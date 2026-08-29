# One stage, unlike the other two examples, because there is nothing to compile
# and a second stage would copy a virtual environment rather than a binary. The
# slim base is the whole difference, and it is worth making on purpose.
FROM python:3.13-slim

# Dependencies first, so a change to a view does not reinstall them.
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY . .

# Not root, and created before the copy so the application cannot rewrite its
# own code at runtime.
RUN useradd --uid 10001 --create-home app && chown -R app:app /app
USER app

EXPOSE 8000
# Bound to every interface, not localhost. Inside a container localhost means
# the container, so a server bound there is a port that is open and that
# nothing outside can reach: a service that looks started and never becomes
# ready.
CMD ["gunicorn", "--bind", "0.0.0.0:8000", "--workers", "2", "--access-logfile", "-", "config.wsgi:application"]
