"""Enough rows that a fresh environment has something to show.

A real repository seeds from the golden. This is what makes the example
runnable with no production database anywhere near it, and it is a data
migration rather than a fixture so that it runs by the same command the
manifest already uses.
"""

from django.db import migrations

CUSTOMERS = [
    ("Ada Lovelace", "ada@example.test", "+44 20 7946 0958"),
    ("Grace Hopper", "grace@example.test", "+1 202 555 0143"),
    ("Katherine Johnson", "katherine@example.test", "+1 202 555 0170"),
    ("Alan Turing", "alan@example.test", "+44 20 7946 0231"),
]

ORDERS = [
    ("ada@example.test", 2599),
    ("ada@example.test", 14900),
    ("grace@example.test", 8250),
    ("grace@example.test", 3199),
    ("katherine@example.test", 47500),
]


def seed(apps, schema_editor):
    Customer = apps.get_model("orders", "Customer")
    Order = apps.get_model("orders", "Order")

    by_email = {}
    for name, email, phone in CUSTOMERS:
        customer, _ = Customer.objects.get_or_create(
            email=email, defaults={"name": name, "phone": phone}
        )
        by_email[email] = customer

    for email, cents in ORDERS:
        Order.objects.create(customer=by_email[email], total_cents=cents)


def unseed(apps, schema_editor):
    """Reversible, because a migration that cannot be undone is one nobody
    dares run twice."""
    Customer = apps.get_model("orders", "Customer")
    Order = apps.get_model("orders", "Order")
    emails = [email for _, email, _ in CUSTOMERS]
    Order.objects.filter(customer__email__in=emails).delete()
    Customer.objects.filter(email__in=emails).delete()


class Migration(migrations.Migration):
    dependencies = [("orders", "0001_initial")]
    operations = [migrations.RunPython(seed, unseed)]
