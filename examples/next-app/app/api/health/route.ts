import { db } from "@/lib/db";

// The health path the manifest names. It answers only once the database
// answers, so "ready" means the whole service is usable rather than that a
// process is listening on a port.
export async function GET() {
  try {
    await db().query("SELECT 1");
  } catch {
    return Response.json({ status: "database unreachable" }, { status: 503 });
  }
  return Response.json({ status: "ok" });
}
