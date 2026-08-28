// A small orders API, kept small on purpose.
//
// It exists so that `af up` has something real to build, branch a database
// for, and run: three endpoints, one Postgres schema, and no framework. Every
// line of it is here because the manifest beside it refers to it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type customer struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type order struct {
	ID         int       `json:"id"`
	CustomerID int       `json:"customer_id"`
	TotalCents int       `json:"total_cents"`
	PlacedAt   time.Time `json:"placed_at"`
	// PaymentIntent is what Stripe called this payment. It is here so that a
	// run of this example shows the outbound call happening rather than only
	// describing it in the manifest.
	PaymentIntent string `json:"payment_intent"`
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set. Antifailure supplies it; outside an environment, export one.")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	mux := http.NewServeMux()

	// The health path the manifest names. It answers only once the database
	// answers, so "ready" means the whole service is usable rather than that
	// a process is listening.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /customers", func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(),
			`SELECT id, name, email FROM customers ORDER BY id LIMIT 100`)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		out := []customer{}
		for rows.Next() {
			var c customer
			if err := rows.Scan(&c.ID, &c.Name, &c.Email); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			out = append(out, c)
		}
		writeJSON(w, out)
	})

	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CustomerID int `json:"customer_id"`
			TotalCents int `json:"total_cents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "body is not JSON", http.StatusBadRequest)
			return
		}
		// Refused rather than stored. An order with no customer is the row the
		// invariant in the manifest exists to catch, and a service that can
		// create one makes that invariant a report rather than a guarantee.
		if in.CustomerID == 0 || in.TotalCents <= 0 {
			http.Error(w, "customer_id and a positive total_cents are required",
				http.StatusUnprocessableEntity)
			return
		}

		// The payment happens before the row, and a failed payment means no
		// order. This is the call the egress rule in the manifest is about:
		// api.stripe.com is the one host this environment may reach, and it
		// is answered by the pack that ships with the engine, so this runs
		// with no Stripe account and no key configured.
		intent, err := createPaymentIntent(r.Context(), in.TotalCents, in.CustomerID)
		if err != nil {
			// Reported rather than swallowed. A payment that did not happen
			// must not look like an order that did, and the egress decision
			// behind a refusal is readable with `af net log`.
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		var o order
		err = pool.QueryRow(r.Context(),
			`INSERT INTO orders (customer_id, total_cents)
			 VALUES ($1, $2) RETURNING id, customer_id, total_cents, placed_at`,
			in.CustomerID, in.TotalCents,
		).Scan(&o.ID, &o.CustomerID, &o.TotalCents, &o.PlacedAt)
		o.PaymentIntent = intent
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(w, o)
	})

	addr := ":" + strconv.Itoa(mustPort(port))
	log.Printf("orders api listening on %s", addr)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

func mustPort(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		log.Fatalf("PORT is not a number: %q", s)
	}
	return n
}

// stripe is the client for the one outbound call this service makes.
//
// It is an ordinary http.Client on the default transport, which reads the
// proxy variables the environment sets. That is the whole integration: no SDK,
// no Antifailure import, nothing in this file knows it is running inside a
// disposable environment. A service that reached Stripe directly in production
// reaches the sidecar here by changing nothing.
var stripe = &http.Client{Timeout: 20 * time.Second}

// createPaymentIntent takes the money for an order and returns Stripe's id
// for the payment.
//
// Outside an environment this talks to Stripe. Inside one it talks to the
// sidecar, which answers from the recorded pack when the rule says mock, or
// substitutes a real key on the way to Stripe's sandbox when the rule says
// sandbox. The difference is a line in antifailure.yaml, not a line here.
func createPaymentIntent(ctx context.Context, totalCents, customerID int) (string, error) {
	form := url.Values{
		"amount":   {strconv.Itoa(totalCents)},
		"currency": {"usd"},
		"customer": {"cus_" + strconv.Itoa(customerID)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.stripe.com/v1/payment_intents", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Sent only when there is one. Under a mock rule there is no key and the
	// pack does not want one; under a sandbox rule the container holds a
	// placeholder and the sidecar swaps in the real value on the way out, so
	// the secret is never inside this process either way.
	if key := os.Getenv("STRIPE_SECRET_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := stripe.Do(req)
	if err != nil {
		return "", fmt.Errorf("reaching Stripe: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("reading Stripe's response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The body is included because the sidecar's refusal explains itself:
		// which rule decided, and what to change. Dropping it here would turn
		// a readable answer back into a bare status code.
		return "", fmt.Errorf("stripe returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var intent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &intent); err != nil {
		return "", fmt.Errorf("stripe returned something that is not JSON: %w", err)
	}
	if intent.ID == "" {
		return "", fmt.Errorf("stripe returned a payment with no id")
	}
	return intent.ID, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "write response:", err)
	}
}
