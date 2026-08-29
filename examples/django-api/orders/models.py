from django.db import models


class Customer(models.Model):
    """A person, and every field on them is something masking has an opinion
    about. See masking.yaml beside this file."""

    name = models.TextField()
    email = models.TextField(unique=True)
    phone = models.TextField(blank=True, default="")
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        db_table = "customers"


class Order(models.Model):
    """An order, and the foreign key that makes the masking interesting.

    customers.id and orders.customer_id have to be remapped to the same new
    value or every join in this application returns nothing. `link: customer`
    in masking.yaml is what keeps them together.
    """

    customer = models.ForeignKey(Customer, on_delete=models.PROTECT, related_name="orders")
    total_cents = models.IntegerField()
    placed_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        db_table = "orders"
        indexes = [models.Index(fields=["customer"], name="orders_customer_id_idx")]
