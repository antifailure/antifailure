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
	"log"
	"net/http"
	"os"
	"strconv"
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
}

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is not set. Antifailure supplies it; outside an environment, export one.")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
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

		var o order
		err := pool.QueryRow(r.Context(),
			`INSERT INTO orders (customer_id, total_cents)
			 VALUES ($1, $2) RETURNING id, customer_id, total_cents, placed_at`,
			in.CustomerID, in.TotalCents,
		).Scan(&o.ID, &o.CustomerID, &o.TotalCents, &o.PlacedAt)
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "write response:", err)
	}
}
