import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";

export const metadata = movedMetadata("/product", "Product — Antifailure");

export default function Page() {
  return <MovedPage to="/product" label="the product overview" />;
}
