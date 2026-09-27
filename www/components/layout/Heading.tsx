import { cn } from "@/lib/cn";
import { HeadingIcon, type HeadingIconName } from "@/components/home/media/icons";
import { CmsText } from "@/components/cms/Editable";
import { sourceRichText } from "@/lib/cms/richtext";

/**
 * A section head.
 *
 * On a wide screen the sticky table of contents names the section, so the head
 * is just the mark and the sentence. Below `xl` that rail is gone, and the mark
 * on its own left a lone glyph floating above a wall of text with nothing to
 * say what part of the argument you had reached. The `label` rides beside the
 * mark at those widths and carries the name the rail would have carried.
 */
export function Heading({
  title,
  label,
  theme = "light",
  icon,
  className,
  cmsKey,
  sectionId,
}: {
  title: string;
  label?: string;
  theme?: "light" | "dark";
  icon?: HeadingIconName;
  className?: string;
  cmsKey?: string;
  sectionId?: string;
}) {
  return (
    <div
      className={cn(
        "flex max-w-[960px] flex-col gap-y-14",
        "max-xl:max-w-[800px] max-xl:gap-y-12",
        "max-lg:max-w-xl max-lg:gap-y-6",
        "max-md:max-w-full max-md:gap-y-5",
        className,
      )}
    >
      {icon ? (
        <div className="flex items-center gap-x-3.5 max-md:gap-x-3">
          <HeadingIcon name={icon} />
          {label ? (
            <span
              className={cn(
                "hidden font-mono text-[11px] font-medium uppercase leading-none tracking-[0.14em]",
                "max-xl:inline-block max-md:text-[10px]",
                theme === "light" ? "text-black/60" : "text-gray-new-50",
              )}
            >
              {cmsKey && sectionId ? <CmsText cmsKey={`${cmsKey}.label`} label="Section label" sectionId={sectionId} defaultValue={label} /> : label}
            </span>
          ) : null}
        </div>
      ) : null}
      {cmsKey && sectionId ? <CmsText as="h2" cmsKey={cmsKey} label="Section heading" sectionId={sectionId} defaultValue={sourceRichText(title)}
        className={cn(
          "text-[42px] font-normal leading-dense tracking-tighter text-pretty [&>strong]:font-normal",
          "max-xl:text-[36px] max-lg:text-[30px] max-lg:text-wrap max-md:text-[27px]",
          theme === "light" && "text-gray-new-40 [&>strong]:text-black-pure",
          theme === "dark" && "text-gray-new-50 [&>strong]:text-black",
        )}
      /> : <h2
        className={cn(
          "text-[42px] font-normal leading-dense tracking-tighter text-pretty [&>strong]:font-normal",
          "max-xl:text-[36px] max-lg:text-[30px] max-lg:text-wrap max-md:text-[27px]",
          theme === "light" && "text-gray-new-40 [&>strong]:text-black-pure",
          theme === "dark" && "text-gray-new-50 [&>strong]:text-black",
        )}
        dangerouslySetInnerHTML={{ __html: title }}
      />}
    </div>
  );
}
