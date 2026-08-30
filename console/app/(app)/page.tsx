"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { LogoMark } from "@/components/icons";

/**
 * The origin's root.
 *
 * Signed out, Shell renders the sign-in screen and this never runs -- which is
 * why the redirect lives inside Shell's authenticated branch rather than at
 * the top of the file. Signed in, there is no dashboard worth inventing that
 * is not just the environments list, so this is that list one hop early.
 */
function Landing() {
  const router = useRouter();
  useEffect(() => {
    router.replace("/environments");
  }, [router]);
  return (
    <div className="grid min-h-[50vh] place-items-center" role="status">
      <LogoMark className="h-7 w-7 opacity-40" />
      <span className="sr-only">Opening environments</span>
    </div>
  );
}

export default function Home() {
  return <Landing />;
}
