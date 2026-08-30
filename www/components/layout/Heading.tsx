import { cn } from "@/lib/cn";
import { HeadingIcon } from "@/components/home/media/icons";

export function Heading({
  title,
  theme = "light",
  icon,
  className,
}: {
  title: string;
  theme?: "light" | "dark";
  icon?: "migrations" | "workload" | "twins" | "firewall" | "features";
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex max-w-[960px] flex-col gap-y-14",
        "max-xl:max-w-[800px] max-xl:gap-y-12",
        "max-lg:max-w-xl max-lg:gap-y-7",
        "max-md:max-w-full",
        className,
      )}
    >
      {icon ? <HeadingIcon name={icon} /> : null}
      <h2
        className={cn(
          "indent-24 text-[48px] font-normal leading-dense tracking-tighter text-pretty [&>strong]:font-normal",
          "max-xl:text-[40px] max-lg:indent-16 max-lg:text-[28px] max-lg:text-wrap max-md:indent-0 max-md:text-[24px]",
          theme === "light" && "text-gray-new-40 [&>strong]:text-black-pure",
          theme === "dark" && "text-gray-new-50 [&>strong]:text-black",
        )}
        dangerouslySetInnerHTML={{ __html: title }}
      />
    </div>
  );
}
