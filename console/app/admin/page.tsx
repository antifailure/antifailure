"use client";

import Link from "next/link";
import { Card } from "@/components/ui";
import { AdminPage } from "@/components/admin/primitives";
import { ADMIN_NAV, type AdminNavItem } from "@/lib/admin-nav";
import { operatorMay, useAdminContext } from "@/lib/admin";

/**
 * Where an operator lands.
 *
 * A DIRECTORY, NOT A DASHBOARD, and that is the decision this page is. The
 * obvious front page for an operator portal is a wall of numbers, and it is the
 * single most expensive thing this one could ship: half the sections behind it
 * are not built, so the numbers would be zeroes, and a zero on this page is
 * indistinguishable from an answer. An operator during an incident reading "0
 * failing runs" off a placeholder has been lied to by their own tooling.
 *
 * What this page does instead is the thing a portal of twenty two sections
 * actually needs and usually lacks: it says what is here. Every entry names
 * what it answers, so somebody who has not used the portal in a month can find
 * the section they want without opening five of them.
 *
 * FILTERED TO THE ROLE, the same as the rail, from the same list. An operator
 * is never shown a door their role cannot open, and this page and the rail
 * cannot disagree about which those are because there is one list.
 *
 * WHEN THE SECTIONS EXIST, this is the page that should grow a real summary of
 * the installation, measured rather than estimated, above the directory. Not
 * before.
 */
export default function AdminOverviewPage() {
  const { me } = useAdminContext();

  const groups = ADMIN_NAV.map((group) => ({
    ...group,
    items: group.items.filter((item) => operatorMay(me, item.permission)),
  })).filter((group) => group.items.length > 0);

  return (
    <AdminPage
      href="/admin"
      lede={
        me
          ? `Signed in as ${me.email}, with the ${me.role.replace(/_/g, " ")} role. Everything below is what that role can reach.`
          : undefined
      }
    >
      {groups.length === 0 ? (
        <Card>
          <div className="px-6 py-12 text-center">
            <p className="text-[14px] font-medium text-ink">Your role opens nothing here yet</p>
            <p className="mx-auto mt-2 max-w-[52ch] text-[13px] leading-6 text-muted">
              You can sign in to the portal, and no section is granted to your role. An operator
              who can grant permissions can change that from Admins &amp; Permissions.
            </p>
          </div>
        </Card>
      ) : (
        // items-start, so a card ends where its content ends. Grid items
        // stretch to the tallest in their row by default, and the groups have
        // three entries and five, so Customers was drawn as tall as Product
        // with two hundred pixels of nothing inside it. That reads as a
        // section that failed to load rather than as a short one.
        <div className="grid items-start gap-5 lg:grid-cols-2">
          {groups.map((group) => (
            <Card key={group.slug} title={group.label}>
              <ul>
                {group.items.map((item) => (
                  <SectionLink key={item.href} item={item} />
                ))}
              </ul>
            </Card>
          ))}
        </div>
      )}
    </AdminPage>
  );
}

/**
 * One section, as a row somebody can hit with a thumb.
 *
 * The whole row is the anchor rather than the label being a link inside it, so
 * there is exactly one focusable target per section and the tab order through
 * this page is the reading order. The icon is the same glyph the rail draws, so
 * finding a section here teaches you where it is there.
 */
function SectionLink({ item }: { item: AdminNavItem }) {
  const { Icon } = item;
  return (
    <li className="border-b border-rule last:border-b-0">
      <Link
        href={item.href}
        className="flex min-h-11 items-start gap-3 px-4 py-3 transition-colors hover:bg-[rgba(16,16,16,0.035)]"
      >
        <Icon className="mt-0.5 h-4 w-4 shrink-0 text-muted" />
        <span className="min-w-0">
          <span className="block text-[13.5px] font-medium leading-5 text-ink">{item.label}</span>
          <span className="mt-0.5 block text-[12.5px] leading-5 text-muted">{item.summary}</span>
        </span>
      </Link>
    </li>
  );
}
