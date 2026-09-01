import { db } from "@/lib/db";

// Rendered per request rather than at build time. Without this, Next tries to
// prerender the page while building the image, where there is no database, and
// the build fails on a connection refused that looks nothing like its cause.
// A page that reads a database in a preview environment is dynamic by nature,
// so it says so.
export const dynamic = "force-dynamic";

type Row = {
  id: number;
  name: string;
  email: string;
  orders: number;
  spent_cents: number;
};

const money = new Intl.NumberFormat("en-US", { style: "currency", currency: "USD" });

async function customers(): Promise<Row[]> {
  // The join is the point. Masking rewrites customers.email and
  // orders.customer_id, and both carry `link: customer` in masking.yaml so
  // they are rewritten consistently. If they were not, this query would return
  // every customer with zero orders and the page would look empty rather than
  // wrong, which is the failure the linked transform exists to prevent.
  const { rows } = await db().query<Row>(`
    SELECT c.id,
           c.name,
           c.email,
           COUNT(o.id)::int              AS orders,
           COALESCE(SUM(o.total_cents), 0)::int AS spent_cents
    FROM customers c
    LEFT JOIN orders o ON o.customer_id = c.id
    GROUP BY c.id, c.name, c.email
    ORDER BY c.id
    LIMIT 100
  `);
  return rows;
}

export default async function Page() {
  const rows = await customers();

  return (
    <main>
      <h1>Orders</h1>
      <p className="lede">
        Every row here came from a branch of a masked golden, created for this
        environment and thrown away with it. The names and addresses are not
        real. The shape of the data is.
      </p>

      {rows.length === 0 ? (
        <div className="empty">
          <strong>No customers yet</strong>
          The migration puts customers here, so an empty table usually means it
          did not run. Start with <code>af logs web migration</code>.
        </div>
      ) : (
        <div className="scroller">
          <table>
            <caption>{rows.length} customers, with what each has spent.</caption>
            <thead>
              <tr>
                <th scope="col">Customer</th>
                <th scope="col" className="col-email">Email</th>
                <th scope="col" className="num">Orders</th>
                <th scope="col" className="num">Spent</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id}>
                  <td>
                    {r.name}
                    {/* The same address again, shown only on a narrow screen,
                        where the column it normally lives in is hidden. It is
                        the widest column and the least important, and keeping
                        it as a column there forced the table to scroll with
                        Spent, the number people came for, off the edge. */}
                    <span className="email-inline">{r.email}</span>
                  </td>
                  <td className="email col-email">{r.email}</td>
                  <td className="num">{r.orders}</td>
                  <td className="num">{money.format(r.spent_cents / 100)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <footer>
        Tear this environment down with <code>af down</code>. Everything above
        goes with it, including the database branch.
      </footer>
    </main>
  );
}
