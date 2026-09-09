"""What each published stack asks for that a manifest cannot say."""
import sys, os, yaml, collections, json
sys.path.insert(0, os.path.dirname(__file__))
from compose2af import flatten_extends, load_env

# Every key a manifest service can carry, from schemas/manifest.v1.json.
MANIFEST_SERVICE_KEYS = {
    "name","path","kind","build","command","port","health_path","health_timeout",
    "env","replicas","resources","schedule","migrate","depends_on"}

# The compose keys that DO map, and what they map to.
MAPS = {
    "image": "build.strategy: image, build.image",
    "command": "command (see the entrypoint note)",
    "environment": "env[]",
    "depends_on": "depends_on",
    "ports": "port, and only ONE of them",
    "build": "build.strategy: dockerfile",
}

def hc_kind(hc):
    if not hc: return None
    test = hc.get("test")
    if isinstance(test, str): test = ["CMD-SHELL", test]
    if not test: return None
    joined = " ".join(str(x) for x in test)
    if "/dev/tcp/" in joined: return "a bare TCP connect"
    if "http" in joined.lower():
        if "-H " in joined or "Authorization" in joined: return "an HTTP GET that needs a header"
        return "an HTTP GET on a path"
    return "a command inside the container"

def go(path, name, base=None, envfile=None):
    d = yaml.safe_load(open(path))
    b = yaml.safe_load(open(base)) if base else None
    svcs = flatten_extends(d.get("services") or {}, (b or {}).get("services"))
    env = load_env(envfile)
    unmapped = collections.defaultdict(list)
    binds, namedvols, multiport, hcs = [], [], [], collections.Counter()
    hc_detail = []
    for n, s in svcs.items():
        s = s or {}
        for k in s:
            if k not in MAPS:
                unmapped[k].append(n)
        for v in (s.get("volumes") or []):
            src = v.split(":")[0] if isinstance(v, str) else str(v)
            (binds if (src.startswith("./") or src.startswith("/") or src.startswith("$")) else namedvols).append(f"{n}:{src}")
        if len(s.get("ports") or []) > 1:
            multiport.append(n)
        k = hc_kind(s.get("healthcheck"))
        if k:
            hcs[k] += 1
            hc_detail.append((n, k))
    print(f"\n########## {name}: {len(svcs)} declared services")
    print(f"  bind mounts of a file or directory from the repository: {len(binds)}")
    for x in binds: print(f"      {x}")
    print(f"  named docker volumes: {len(namedvols)}")
    for x in namedvols: print(f"      {x}")
    print(f"  services declaring more than one published port: {len(multiport)} {multiport}")
    print(f"  readiness predicates, by what they actually are:")
    for k, c in hcs.most_common(): print(f"      {c:2d}  {k}")
    print(f"  compose keys with NO manifest key at all:")
    for k, v in sorted(unmapped.items(), key=lambda kv: -len(kv[1])):
        print(f"      {k:16s} {len(v):3d} services")
    named_db = [n for n in svcs if n == "db"]
    print(f"  services named 'db', which is the engine's own Postgres alias: {named_db}")

U = "/private/tmp/af-stacks-l91/upstream"
go(f"{U}/ch-1S_1K.yaml", "ClickHouse ch-1S_1K, from ClickHouse/examples")
go(f"{U}/supabase.yml", "Supabase self hosting, from supabase/supabase docker/", envfile=f"{U}/supabase.env.example")
go(f"{U}/posthog-hobby.yml", "PostHog hobby, from PostHog/posthog", base=f"{U}/posthog-base.yml")
