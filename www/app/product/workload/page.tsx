import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";
import { pageTitle } from "@/lib/site";

export const metadata = movedMetadata("/product/load", pageTitle("Load"));

export default function Page() {
  return <MovedPage to="/product/load" label="load" />;
}
