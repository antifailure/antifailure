import type { Metadata } from "next";

/** The same string as this screen's heading, so a tab says which screen it is.
 *  Why a layout: the page under it is a client component and cannot export
 *  metadata. See app/layout.tsx. */
export const metadata: Metadata = { title: "Approve a terminal" };

export default function DeviceLayout({ children }: { children: React.ReactNode }) {
  return children;
}
