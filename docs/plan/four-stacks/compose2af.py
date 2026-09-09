"""Convert a published docker compose file into an antifailure.yaml.

Mechanical, and it refuses to be clever. Every service in the compose file
becomes a service in the manifest. Every compose key the manifest has no home
for is recorded in a dropped list rather than dropped quietly, because a
conversion that silently loses a bind mount produces a twin that starts and is
not the thing it is a twin of.
"""
import sys, os, re, json, collections
sys.path.insert(0, os.path.dirname(__file__))
import yaml

# What a manifest service can carry, from schemas/manifest.v1.json.
EXPRESSIBLE = {"image", "command", "ports", "environment", "depends_on", "build"}
# Everything else in a compose service has no manifest key at all.

CRED_HINT = re.compile(r"(KEY|SECRET|TOKEN|PASSWORD|PASSWD|CREDENTIAL)", re.I)

def looks_like_a_credential(name, value):
    """Approximates the engine's own refusal so the conversion does not have to
    be run once per rejected line. The engine is the authority; this only keeps
    the loop short."""
    if not value:
        return False
    if re.match(r"^[a-z+]+://[^/\s]*:[^@/\s]+@", value):
        return True          # a URI carrying a password
    if value.count(".") == 2 and value.startswith("ey"):
        return True          # a JWT
    if CRED_HINT.search(name) and len(value) >= 16:
        return True
    return False

# Compose escapes a literal dollar as $$, so a shell script embedded in a
# command keeps its own variables. Resolving before unescaping turns $$ELAPSED
# into a bare $ and silently breaks the script, which is what this placeholder
# exists to stop.
DOLLAR = "\x00AF-LITERAL-DOLLAR\x00"

def resolve(v, env):
    """Resolve ${VAR:-default} and $VAR the way compose does, no further."""
    if not isinstance(v, str):
        return v
    v = v.replace("$$", DOLLAR)
    def sub(m):
        name = m.group("name")
        default = m.group("default")
        if name in env and env[name] != "":
            return env[name]
        return default if default is not None else ""
    v = re.sub(r"\$\{(?P<name>[A-Za-z_][A-Za-z0-9_]*)(?::?-(?P<default>[^}]*))?\}", sub, v)
    v = re.sub(r"\$(?P<name>[A-Za-z_][A-Za-z0-9_]*)", lambda m: env.get(m.group("name"), ""), v)
    return v.replace(DOLLAR, "$")

def load_env(path):
    env = {}
    if not path or not os.path.exists(path):
        return env
    for line in open(path):
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, _, val = line.partition("=")
        env[k.strip()] = val.strip()
    return env

def flatten_extends(services, base_services):
    """Compose `extends` merges the base service under the local one."""
    out = {}
    for name, svc in services.items():
        svc = dict(svc or {})
        ext = svc.pop("extends", None)
        if ext:
            base = dict((base_services or {}).get(ext.get("service", name)) or {})
            merged = dict(base)
            for k, v in svc.items():
                if k == "environment" and isinstance(v, list) and isinstance(merged.get(k), list):
                    merged[k] = list(merged[k]) + v
                else:
                    merged[k] = v
            svc = merged
        out[name] = svc
    return out

def env_pairs(node, env):
    pairs = []
    if isinstance(node, dict):
        for k, v in node.items():
            pairs.append((k, resolve("" if v is None else str(v), env)))
    elif isinstance(node, list):
        for item in node:
            k, _, v = str(item).partition("=")
            pairs.append((k, resolve(v, env)))
    # Compose lets a later entry win for the same key, and the manifest refuses
    # a name declared twice. Keeping the last is what compose would have run.
    seen = {}
    for k, v in pairs:
        seen[k] = v
    return list(seen.items())

def first_port(svc, env):
    """The container port a manifest can name. Compose publishes host:container."""
    ports = svc.get("ports") or []
    for p in ports:
        p = resolve(str(p), env)
        parts = p.split("/")[0].split(":")
        try:
            return int(parts[-1])
        except ValueError:
            continue
    return None

def convert(compose_path, env_path, base_path, name):
    d = yaml.safe_load(open(compose_path))
    base = yaml.safe_load(open(base_path)) if base_path else None
    env = load_env(env_path)
    services = flatten_extends(d.get("services") or {}, (base or {}).get("services"))

    dropped = collections.defaultdict(list)
    dotenv = {}
    collisions = []
    out_services = []
    for sname, svc in services.items():
        svc = svc or {}
        for k in svc:
            if k not in EXPRESSIBLE:
                dropped[k].append(sname)
        image = resolve(svc.get("image") or "", env)
        entry = {"name": re.sub(r"[^a-z0-9-]", "-", sname.lower())}
        port = first_port(svc, env)
        if port:
            entry["kind"] = "web"
            entry["port"] = port
        else:
            entry["kind"] = "worker"
        cmd = svc.get("command")
        if isinstance(cmd, list):
            entry["command"] = " ".join(resolve(str(c), env) for c in cmd)
        elif isinstance(cmd, str):
            entry["command"] = resolve(cmd, env)
        dep = svc.get("depends_on")
        if isinstance(dep, dict):
            dep = list(dep.keys())
        if dep:
            entry["depends_on"] = [re.sub(r"[^a-z0-9-]", "-", x.lower()) for x in dep]
        if image:
            entry["build"] = {"strategy": "image", "image": image}
        elif svc.get("build"):
            entry["build"] = {"strategy": "dockerfile"}
            dropped["build(local source, not published as an image)"].append(sname)
        ev = env_pairs(svc.get("environment"), env)
        if ev:
            out_env = []
            for k, v in ev:
                if looks_like_a_credential(k, v):
                    # The manifest refuses a literal credential, correctly: it is
                    # a committed file. The value moves to .env, which is the
                    # place the engine looks before the encrypted store.
                    prev = dotenv.get(k)
                    if prev is not None and prev != v:
                        collisions.append((k, sname))
                    dotenv[k] = v
                    out_env.append({"name": k})
                else:
                    out_env.append({"name": k, "value": v})
            entry["env"] = out_env
        out_services.append(entry)

    manifest = {"version": 1, "name": name, "services": out_services,
                "egress": {"default": "block"}}
    return manifest, dropped, services, dotenv, collisions

if __name__ == "__main__":
    import argparse
    ap = argparse.ArgumentParser()
    ap.add_argument("compose"); ap.add_argument("--env"); ap.add_argument("--base")
    ap.add_argument("--name", required=True); ap.add_argument("-o", required=True)
    a = ap.parse_args()
    m, dropped, services, dotenv, collisions = convert(a.compose, a.env, a.base, a.name)
    with open(os.path.join(os.path.dirname(a.o) or ".", ".env"), "w") as f:
        for k, v in dotenv.items():
            f.write(f"{k}={v}\n")
    with open(a.o, "w") as f:
        yaml.safe_dump(m, f, sort_keys=False, width=100)
    print(f"{len(m['services'])} services written to {a.o}")
    print(f"{len(dotenv)} credential shaped values moved out of the manifest into .env")
    if collisions:
        print("SAME NAME, DIFFERENT VALUE, and .env is one flat namespace:")
        for k, s2 in collisions:
            print(f"  {k} (redeclared by {s2})")
    print("compose keys with no manifest home:")
    for k, v in sorted(dropped.items(), key=lambda kv: -len(kv[1])):
        print(f"  {k}: {len(v)} services -> {', '.join(v[:6])}{' ...' if len(v)>6 else ''}")
