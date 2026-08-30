import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";

export const metadata = movedMetadata("/product/load", "Load — Antifailure");

export default function Page() {
  return <MovedPage to="/product/load" label="load" />;
}
