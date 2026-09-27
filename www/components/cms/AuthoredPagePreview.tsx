"use client";

import { useEffect, useState } from "react";
import { authoredPage, isAuthoredPagePath } from "@antifailure/website";
import { useCms } from "./CmsProvider";
import { AuthoredPageContent } from "./AuthoredPage";

export function AuthoredPagePreview() {
  const cms = useCms();
  const [path, setPath] = useState("");
  useEffect(() => {
    const value = new URLSearchParams(window.location.search).get("path");
    if (isAuthoredPagePath(value)) setPath(value);
  }, []);
  const page = authoredPage(cms.document, path);
  if (!page) return <div className="safe-paddings py-24 text-base text-gray-new-40">Loading this page’s draft…</div>;
  return <AuthoredPageContent page={page} />;
}
