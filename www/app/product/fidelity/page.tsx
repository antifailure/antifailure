import { MovedPage, movedMetadata } from "@/components/layout/MovedPage";
import { pageTitle } from "@/lib/site";

export const metadata = movedMetadata("/product/report", pageTitle("Safety Report"));

export default function Page() {
  return <MovedPage to="/product/report" label="the safety report" />;
}
