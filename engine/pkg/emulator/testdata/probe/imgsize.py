#!/usr/bin/env python3
"""Layer sizes for a pinned image, read from the registry rather than from a
daemon. Reports the compressed download and the config's own layer count for
the linux/arm64 and linux/amd64 members of a multi architecture index."""
import json, sys, urllib.request

ACCEPT = ",".join([
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
])

def get(host, repo, ref, token=None):
    r = urllib.request.Request(f"https://{host}/v2/{repo}/manifests/{ref}",
                               headers={"Accept": ACCEPT})
    if token:
        r.add_header("Authorization", "Bearer " + token)
    with urllib.request.urlopen(r, timeout=60) as f:
        return json.load(f)

def hub_token(repo):
    u = f"https://auth.docker.io/token?service=registry.docker.io&scope=repository:{repo}:pull"
    with urllib.request.urlopen(u, timeout=60) as f:
        return json.load(f)["token"]

def report(label, host, repo, digest):
    tok = hub_token(repo) if host == "registry-1.docker.io" else None
    m = get(host, repo, digest, tok)
    out = {}
    if "manifests" in m:
        for entry in m["manifests"]:
            p = entry.get("platform", {})
            plat = f"{p.get('os')}/{p.get('architecture')}"
            if plat not in ("linux/amd64", "linux/arm64"):
                continue
            sub = get(host, repo, entry["digest"], tok)
            total = sum(l["size"] for l in sub.get("layers", []))
            out[plat] = (total, len(sub.get("layers", [])))
    else:
        total = sum(l["size"] for l in m.get("layers", []))
        out["single"] = (total, len(m.get("layers", [])))
    for plat, (total, n) in sorted(out.items()):
        print(f"{label:34s} {plat:14s} {total/1e6:9.1f} MB compressed, {n} layers")

report("fake-gcs-server 1.56.1", "registry-1.docker.io", "fsouza/fake-gcs-server",
       "sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef")
report("google-cloud-cli 583.0.0-emulators", "gcr.io", "google.com/cloudsdktool/google-cloud-cli",
       "sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262")
report("cloud-spanner-emulator 1.5.57", "gcr.io", "cloud-spanner-emulator/emulator",
       "sha256:4987860c9f8ecf1fffbbcdac115cb88cb9d1a42bd966c235a9ab843aea34fbd1")
