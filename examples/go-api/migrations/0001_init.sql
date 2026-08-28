-- The whole schema. Two tables and the foreign key between them, which is what
-- makes the masking interesting: customers.id and orders.customer_id have to
-- mask to the same value or every join returns nothing.

CREATE TABLE IF NOT EXISTS customers (
    id         serial PRIMARY KEY,
    name       text        NOT NULL,
    email      text        NOT NULL UNIQUE,
    phone      text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS orders (
    id          serial PRIMARY KEY,
    customer_id integer     NOT NULL REFERENCES customers (id),
    total_cents integer     NOT NULL CHECK (total_cents > 0),
    placed_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS orders_customer_id_idx ON orders (customer_id);

-- Two customers and an order, so a fresh environment has something to show.
-- A real repository's seed comes from the golden; this is what makes the
-- example runnable with no production database anywhere near it.
INSERT INTO customers (name, email, phone) VALUES
    ('Ada Lovelace',    'ada@example.test',    '+44 20 7946 0958'),
    ('Grace Hopper',    'grace@example.test',  '+1 202 555 0143')
ON CONFLICT (email) DO NOTHING;

INSERT INTO orders (customer_id, total_cents)
SELECT id, 2599 FROM customers WHERE email = 'ada@example.test'
ON CONFLICT DO NOTHING;
