import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Orders",
  description: "An example application running inside an Antifailure environment.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
