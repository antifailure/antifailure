-- The whole schema. Two tables and the foreign key between them, which is what
-- makes the masking interesting: customers.id and orders.customer_id have to
-- mask to the same value or the page renders every customer with no orders.

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

-- Enough rows that the page has something to show and the totals differ. A
-- real repository seeds from the golden; this is what makes the example
-- runnable with no production database anywhere near it.
INSERT INTO customers (name, email, phone) VALUES
    ('Ada Lovelace',     'ada@example.test',     '+44 20 7946 0958'),
    ('Grace Hopper',     'grace@example.test',   '+1 202 555 0143'),
    ('Katherine Johnson','katherine@example.test','+1 202 555 0170'),
    ('Alan Turing',      'alan@example.test',    '+44 20 7946 0231')
ON CONFLICT (email) DO NOTHING;

INSERT INTO orders (customer_id, total_cents)
SELECT c.id, v.cents
FROM customers c
JOIN (VALUES
    ('ada@example.test',      2599),
    ('ada@example.test',      14900),
    ('grace@example.test',    8250),
    ('grace@example.test',    3199),
    ('katherine@example.test', 47500)
) AS v (email, cents) ON v.email = c.email
ON CONFLICT DO NOTHING;
