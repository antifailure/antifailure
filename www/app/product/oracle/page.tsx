import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";
import { pageTitle } from "@/lib/site";

export const metadata = movedMetadata("/product", pageTitle("Product"));

export default function Page() {
  return <MovedPage to="/product" label="the product overview" />;
}
