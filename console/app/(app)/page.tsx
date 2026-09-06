"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { LogoMark } from "@/components/icons";
import { useSessionContext } from "@/components/session";
import { useStartChoice } from "@/lib/start-choice";
import { destinationFor } from "@/lib/start";

/**
 * The origin's root.
 *
 * Signed out, Shell renders the sign-in screen and this never runs, which is
 * why the redirect lives inside Shell's authenticated branch rather than at
 * the top of the file. Signed in, this is one of two places: the question
 * asked once after the first sign-in, or the environments list, which is
 * where the root always went and still goes for anybody who has answered.
 *
 * The answer is read from this browser, so nothing is decided until it has
 * been read: deciding on the prerender's empty answer would send a returning
 * person to the question for a frame before sending them on.
 */
function Landing() {
  const router = useRouter();
  const session = useSessionContext();
  const { choice, ready } = useStartChoice(session.data?.orgId);
  useEffect(() => {
    if (!ready) return;
    router.replace(destinationFor(choice));
  }, [router, ready, choice]);
  return (
    <div className="grid min-h-[50vh] place-items-center" role="status">
      <LogoMark className="h-7 w-7 opacity-40" />
      <span className="sr-only">Opening the console</span>
    </div>
  );
}

export default function Home() {
  return <Landing />;
}
