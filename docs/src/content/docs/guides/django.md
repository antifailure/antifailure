---
title: Django
description: Running Django's own migrations against a branch, and the three settings that decide whether it works.
sidebar:
  order: 13
---

Django needs nothing Antifailure specific either, and the interesting part is
what it does not need. The engine does not want a SQL file or a `psql` in your
image. It runs the command you already type:

```yaml
services:
  - name: web
    kind: web
    path: .
    port: 8000
    health_path: /health
    migrate: "python manage.py migrate --noinput"
```

That runs against the branch rather than the golden, so a pull request that
adds a field gets the column and nobody else's environment does.

The working version of everything below is
[`examples/django-api`](https://github.com/antifailure/antifailure/tree/main/examples/django-api).

## Read DATABASE_URL, and fail loudly without it

Antifailure injects `DATABASE_URL` pointing at the branch. Parsing it is the
whole integration:

```python
parsed = urlparse(os.environ["DATABASE_URL"])
DATABASES = {
    "default": {
        "ENGINE": "django.db.backends.postgresql",
        "NAME": parsed.path.lstrip("/"),
        "USER": parsed.username or "",
        "PASSWORD": parsed.password or "",
        "HOST": parsed.hostname or "",
        "PORT": str(parsed.port or 5432),
    }
}
```

Raise if it is absent rather than falling back to a local database. A service
that quietly connects to something else is worse than one that will not start,
because the environment then reports ready and serves the wrong data.

## Configure logging, or a 500 tells you nothing

This is the one that costs an afternoon. Django's default configuration gates
its console handler on `DEBUG` and sends request errors to `mail_admins`. In a
`DEBUG=False` deployment, which is every deployment, an unhandled exception
leaves an access log line reading 500 and nothing else. `af logs web` shows you
the request and not the reason.

```python
LOGGING = {
    "version": 1,
    "disable_existing_loggers": False,
    "handlers": {"console": {"class": "logging.StreamHandler"}},
    "loggers": {
        "django.request": {"handlers": ["console"], "level": "ERROR", "propagate": False},
    },
}
```

Standard output is where `af logs` reads. Without this the engine is showing
you everything Django said, which is nothing.

## ALLOWED_HOSTS, and why the example sets it to everything

The engine reaches a service through the ingress forwarder on the
environment's network, so the host header is not predictable and pinning it
refuses the health check the manifest depends on.

The example sets `ALLOWED_HOSTS = ["*"]` and says at the setting why: an
environment's entire network is sealed by the egress proxy, and nothing outside
it can reach the service at all. That reasoning is true inside an environment
and false in production, so it is the one line in the example not to copy.

## Bind to every interface

```dockerfile
CMD ["gunicorn", "--bind", "0.0.0.0:8000", "config.wsgi:application"]
```

Inside a container `localhost` means that container, so a server bound there is
a port that is open and that nothing outside can reach: a service that looks
started and never becomes ready.

## Seed with a data migration

A fixture needs a second command in the manifest, and a second command is one
more thing to forget. A data migration runs by the command already there:

```python
class Migration(migrations.Migration):
    dependencies = [("orders", "0001_initial")]
    operations = [migrations.RunPython(seed, unseed)]
```

Make it reversible. A migration nobody dares run twice is a migration nobody
runs.

Related: [building services](/docs/guides/build), [masking](/docs/concepts/masking).
