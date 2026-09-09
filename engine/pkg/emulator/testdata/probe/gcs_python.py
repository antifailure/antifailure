# The vendor's own SDK, unmodified, in a second language.
#
# As with the JavaScript probe, the point is what is absent: no client_options,
# no api_endpoint, no STORAGE_EMULATOR_HOST, no _http override. The client is
# built the way production builds it. Only the two things an environment sets
# around a process differ: a proxy and a certificate authority to trust.
import json, os, sys, time
from google.cloud import storage

out = {"library": "google-cloud-storage", "version": storage.__version__, "steps": []}

def step(name, fn):
    t0 = time.time()
    try:
        v = fn()
        out["steps"].append({"name": name, "ok": True,
                             "ms": int((time.time() - t0) * 1000), "value": v})
    except Exception as e:
        out["steps"].append({"name": name, "ok": False,
                             "ms": int((time.time() - t0) * 1000),
                             "error": f"{type(e).__name__}: {e}"[:300]})
        print(json.dumps(out, indent=2)); sys.exit(1)

client = storage.Client(project=os.environ.get("GOOGLE_CLOUD_PROJECT"))
bucket_name = "af-l33-probe-py"

step("createBucket", lambda: client.create_bucket(bucket_name).name)
step("upload", lambda: (client.bucket(bucket_name).blob("hello.txt")
                        .upload_from_string("the twin wrote this",
                                            content_type="text/plain"), "hello.txt")[1])
step("download", lambda: client.bucket(bucket_name).blob("hello.txt")
     .download_as_bytes().decode())
step("list", lambda: [b.name for b in client.list_blobs(bucket_name)])
print(json.dumps(out, indent=2))
