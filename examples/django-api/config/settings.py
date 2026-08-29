"""Settings for the example, kept to what the example actually uses.

The interesting line is DATABASES. Antifailure injects DATABASE_URL pointing at
a branch of a masked golden, created for this environment and destroyed with
it. Nothing else here knows that: it is the same setting a production deploy
would have, reading the same variable.
"""

import os
from pathlib import Path
from urllib.parse import urlparse

BASE_DIR = Path(__file__).resolve().parent.parent

# Read from the environment rather than committed. The example runs with the
# value Antifailure supplies; outside an environment, export one.
SECRET_KEY = os.environ.get("DJANGO_SECRET_KEY", "insecure-example-key-not-for-production")
DEBUG = False

# The engine reaches the service through the ingress forwarder on the
# environment's network, so the host header is not predictable and pinning it
# would refuse the health check the manifest depends on. This is an example
# whose entire network is sealed by the proxy, and it is the one setting here
# that would be wrong to copy into production unchanged.
ALLOWED_HOSTS = ["*"]

INSTALLED_APPS = [
    "django.contrib.contenttypes",
    "django.contrib.auth",
    "orders",
]

MIDDLEWARE = ["django.middleware.common.CommonMiddleware"]

ROOT_URLCONF = "config.urls"
WSGI_APPLICATION = "config.wsgi.application"


def _database_from_url(url: str) -> dict:
    """Turn a connection string into Django's DATABASES entry.

    Written out rather than pulled from a helper package, because a reader
    should be able to see exactly what happens to the value the engine hands
    over: it is parsed, and nothing else.
    """
    parsed = urlparse(url)
    return {
        "ENGINE": "django.db.backends.postgresql",
        "NAME": parsed.path.lstrip("/"),
        "USER": parsed.username or "",
        "PASSWORD": parsed.password or "",
        "HOST": parsed.hostname or "",
        "PORT": str(parsed.port or 5432),
        # Reconnecting per request keeps this simple and correct against a
        # database that can be branched and destroyed underneath it.
        "CONN_MAX_AGE": 0,
    }


DATABASE_URL = os.environ.get("DATABASE_URL")
if not DATABASE_URL:
    raise RuntimeError(
        "DATABASE_URL is not set. Antifailure supplies it; outside an environment, export one."
    )
DATABASES = {"default": _database_from_url(DATABASE_URL)}

# Exceptions go to stdout, which is where `af logs web` reads.
#
# Django's default configuration sends request errors to the mail_admins
# handler and gates its console handler on DEBUG, so a 500 in a DEBUG=False
# deployment leaves nothing but an access log line saying 500. That is exactly
# the state this example was in the first time it failed, and it made a
# one line bug take a rebuild to find.
LOGGING = {
    "version": 1,
    "disable_existing_loggers": False,
    "handlers": {"console": {"class": "logging.StreamHandler"}},
    "loggers": {
        "django.request": {"handlers": ["console"], "level": "ERROR", "propagate": False},
    },
}

DEFAULT_AUTO_FIELD = "django.db.models.AutoField"
USE_TZ = True
