import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";

export const metadata = movedMetadata("/product/report", "Safety Report — Antifailure");

export default function Page() {
  return <MovedPage to="/product/report" label="the safety report" />;
}
