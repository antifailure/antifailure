package redact_test

import (
	"fmt"
	"math/rand"
	"strings"
)

// The corpus is generated from a fixed seed rather than committed as a file,
// so that it can be large without bloating the repository and so that no
// reviewer ever has to look at three hundred credential shaped strings and
// wonder whether one of them is real. Nothing here is a real credential; the
// bodies are drawn from a seeded generator.

const alnum = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const lowerHex = "0123456789abcdef"
const upperAlnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const b64url = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"

func body(rnd *rand.Rand, alphabet string, n int) string {
	var b strings.Builder
	b.Grow(n)
	for i := 0; i < n; i++ {
		b.WriteByte(alphabet[rnd.Intn(len(alphabet))])
	}
	return b.String()
}

// secretCorpus returns credential shaped strings that must all be redacted,
// paired with the name of the format so a failure names what broke.
func secretCorpus() []struct{ Name, Value string } {
	rnd := rand.New(rand.NewSource(20260825))
	var out []struct{ Name, Value string }
	add := func(name, v string) {
		out = append(out, struct{ Name, Value string }{name, v})
	}

	for i := 0; i < 8; i++ {
		add("stripe-secret-live", stripeSecretLive+body(rnd, alnum, 24+i))
		add("stripe-secret-test", "sk"+"_test_"+body(rnd, alnum, 24+i))
		add("stripe-restricted", "rk"+"_live_"+body(rnd, alnum, 30+i))
		add("stripe-publishable", stripePublicLive+body(rnd, alnum, 24+i))
		add("stripe-webhook", "wh"+"sec_"+body(rnd, alnum, 32))
		add("github-classic", githubClassic+body(rnd, alnum, 36))
		add("github-oauth", "gh"+"o_"+body(rnd, alnum, 36))
		add("github-user-server", "gh"+"u_"+body(rnd, alnum, 36))
		add("github-server", "gh"+"s_"+body(rnd, alnum, 36))
		add("github-refresh", "gh"+"r_"+body(rnd, alnum, 36))
		add("github-fine-grained", "github"+"_pat_"+body(rnd, alnum, 22)+"_"+body(rnd, alnum, 59))
		add("aws-access-key", "AK"+"IA"+body(rnd, upperAlnum, 16))
		add("aws-session-key", "AS"+"IA"+body(rnd, upperAlnum, 16))
		add("slack-bot", "xo"+"xb-"+body(rnd, "0123456789", 12)+"-"+body(rnd, alnum, 24))
		add("slack-user", "xo"+"xp-"+body(rnd, "0123456789", 12)+"-"+body(rnd, alnum, 24))
		add("sendgrid", "S"+"G."+body(rnd, b64url, 22)+"."+body(rnd, b64url, 43))
		add("openai", "sk"+"-"+body(rnd, alnum, 48))
		add("openai-project", "sk"+"-proj-"+body(rnd, b64url, 48))
		add("anthropic", "sk"+"-ant-api03-"+body(rnd, b64url, 90))
		add("google-api", "AI"+"za"+body(rnd, b64url, 35))
		add("twilio-sid", "AC"+body(rnd, lowerHex, 32))
		add("supabase-service", "sb"+"p_"+body(rnd, lowerHex, 40))
		add("neon-api", "na"+"pi_"+body(rnd, "abcdefghijklmnopqrstuvwxyz0123456789", 28))
		add("npm", "np"+"m_"+body(rnd, alnum, 36))
		add("doppler-service", "dp"+".st."+body(rnd, alnum, 40))
		add("jwt", "ey"+"J"+body(rnd, b64url, 30)+"."+body(rnd, b64url, 60)+"."+body(rnd, b64url, 43))
		add("bearer-header", "Authorization: Bearer "+body(rnd, b64url, 40))
		add("basic-header", "authorization: Basic "+body(rnd, b64url, 40))
		add("api-key-header", "X-Api-Key: "+body(rnd, alnum, 40))
		add("x-auth-token", "x-auth-token: "+body(rnd, alnum, 40))
		add("postgres-url", fmt.Sprintf("postgres://app:%s@db.internal:5432/main", body(rnd, alnum, 24)))
		add("postgresql-url", fmt.Sprintf("postgresql://u:%s@127.0.0.1:5432/x?sslmode=require", body(rnd, alnum, 20)))
		add("redis-url", fmt.Sprintf("redis://default:%s@cache:6379", body(rnd, alnum, 24)))
		add("amqp-url", fmt.Sprintf("amqps://svc:%s@broker:5671/vhost", body(rnd, alnum, 20)))
		add("libpq-password", fmt.Sprintf("host=db user=app password=%s dbname=main", body(rnd, alnum, 20)))
		add("assignment-secret", fmt.Sprintf("CLIENT_SECRET=%s", body(rnd, alnum, 40)))
		add("assignment-token", fmt.Sprintf(`{"access_token": "%s"}`, body(rnd, alnum, 40)))
		add("assignment-apikey", fmt.Sprintf("api_key: %s", body(rnd, alnum, 32)))
		add("assignment-private-key", fmt.Sprintf("private_key = %s", body(rnd, b64url, 44)))
		add("assignment-passwd", fmt.Sprintf("passwd='%s'", body(rnd, alnum, 24)))
	}
	add("pem-rsa", "-----BEGIN RSA PRIVATE KEY-----\n"+body(rnd, b64url, 64)+"\n-----END RSA PRIVATE KEY-----")
	add("pem-ec", "-----BEGIN EC PRIVATE KEY-----\n"+body(rnd, b64url, 64)+"\n-----END EC PRIVATE KEY-----")
	add("pem-openssh", "-----BEGIN OPENSSH PRIVATE KEY-----\n"+body(rnd, b64url, 64)+"\n-----END OPENSSH PRIVATE KEY-----")
	add("pem-pkcs8", "-----BEGIN PRIVATE KEY-----\n"+body(rnd, b64url, 64)+"\n-----END PRIVATE KEY-----")
	add("pem-header-alone", "-----BEGIN RSA PRIVATE KEY-----")
	return out
}

// benignCorpus returns strings that look credential adjacent and must survive
// untouched. Every false positive here is output an operator stops trusting.
func benignCorpus() []struct{ Name, Value string } {
	rnd := rand.New(rand.NewSource(19700101))
	var out []struct{ Name, Value string }
	add := func(name, v string) {
		out = append(out, struct{ Name, Value string }{name, v})
	}

	for i := 0; i < 12; i++ {
		add("uuid-v4", fmt.Sprintf("%s-%s-4%s-a%s-%s",
			body(rnd, lowerHex, 8), body(rnd, lowerHex, 4), body(rnd, lowerHex, 3),
			body(rnd, lowerHex, 3), body(rnd, lowerHex, 12)))
		add("sha256", body(rnd, lowerHex, 64))
		add("sha1-git", body(rnd, lowerHex, 40))
		add("image-digest", "sha256:"+body(rnd, lowerHex, 64))
		add("env-id", "env_"+body(rnd, "abcdefghijklmnopqrstuvwxyz0123456789", 16))
		add("golden-version", "gv_20260825120000_"+body(rnd, lowerHex, 8))
		add("container-id", body(rnd, lowerHex, 64))
		add("k8s-name", "antifailure-env-"+body(rnd, "abcdefghijklmnopqrstuvwxyz0123456789", 12))
		add("base64-image-fragment", "iVBORw0KGgoAAAANSUhEUg"+body(rnd, b64url, 200))
		add("stack-frame", "goroutine 42 [running]: main.run(0x14000123abc, 0x140001240)")
		add("url-no-credential", "https://api.example.test/v1/customers?limit=100&starting_after=cus_"+body(rnd, alnum, 14))
		add("semver", fmt.Sprintf("v1.%d.%d-rc.%d", i, i*3, i+1))
		add("timestamp", fmt.Sprintf("2026-08-25T12:%02d:%02d.123456789Z", i, i*2))
		add("ipv4-port", fmt.Sprintf("10.0.%d.%d:5432", i, i*7))
		add("ipv6", "2001:db8:85a3:0:0:8a2e:370:7334")
		add("file-path", "/work/antifailure/engine/internal/masking/compiler_test.go:1284")
		add("sql-normalized", "SELECT id, email FROM users WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2")
		add("prose-password-policy", "The password policy requires twelve characters and is enforced at signup")
		add("prose-token-count", "The workflow used 18432 prompt tokens and 921 completion tokens")
		add("header-content-type", "Content-Type: application/json; charset=utf-8")
		add("header-user-agent", "User-Agent: antifailure/0.1.0 (linux; amd64) Go-http-client/2.0")
		add("header-etag", `ETag: W/"`+body(rnd, lowerHex, 32)+`"`)
		add("json-metrics", fmt.Sprintf(`{"p50_ms":%d,"p95_ms":%d,"rps":%d}`, i+3, i*9+40, i*100))
		add("already-redacted", "authorization: Bearer [redacted]")
		add("short-hex", body(rnd, lowerHex, 8))
	}
	return out
}
