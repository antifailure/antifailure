export function HeroDemoVideo() {
  return (
    <div className="mx-auto mt-20 max-w-[1180px] max-xl:mt-16 max-md:mt-12">
      <div className="relative overflow-hidden rounded-[28px] border border-black/[0.08] bg-[#f3f2ec] p-2 shadow-[0_28px_90px_rgba(0,0,0,0.08)] max-md:rounded-[20px]">
        <div
          className="pointer-events-none absolute inset-0 opacity-70"
          style={{
            background:
              "radial-gradient(circle at 16% 10%, rgba(102,143,93,0.12), transparent 30%), radial-gradient(circle at 88% 76%, rgba(180,165,116,0.12), transparent 34%)",
          }}
          aria-hidden
        />
        <div className="relative overflow-hidden rounded-[22px] border border-black/[0.08] bg-white max-md:rounded-[16px]">
          <div className="flex h-10 items-center justify-between border-b border-black/[0.07] bg-white/88 px-4 max-sm:h-9 max-sm:px-3">
            <div className="flex items-center gap-2">
              <span className="size-2 rounded-full bg-[#668f5d]" aria-hidden />
              <span className="text-[12px] font-medium tracking-tight text-black/62">
                Antifailure product demo
              </span>
            </div>
            <span className="text-[11px] font-medium tracking-tight text-black/36">
              Safe run preview
            </span>
          </div>
          <div className="bg-black/[0.03] p-1.5">
            <video
              className="block aspect-video w-full rounded-[16px] bg-white object-cover max-md:rounded-[12px]"
              src="/home/option-4.mp4"
              autoPlay
              muted
              loop
              playsInline
              preload="metadata"
              aria-label="A product demo video showing Antifailure validating a deployment before release."
            />
          </div>
        </div>
      </div>
    </div>
  );
}
