import json

from django.db import connection
from django.db.models import Count, Sum
from django.http import HttpRequest, HttpResponse, JsonResponse
from django.views.decorators.csrf import csrf_exempt
from django.views.decorators.http import require_http_methods

from .models import Customer, Order


@require_http_methods(["GET"])
def health(request: HttpRequest) -> HttpResponse:
    """The health path the manifest names.

    It answers only once the database answers, so "ready" means the whole
    service is usable rather than that a process is listening on a port.
    """
    try:
        with connection.cursor() as cursor:
            cursor.execute("SELECT 1")
    except Exception:  # noqa: BLE001 - any failure to reach it means not ready
        return JsonResponse({"status": "database unreachable"}, status=503)
    return JsonResponse({"status": "ok"})


@require_http_methods(["GET"])
def customers(request: HttpRequest) -> HttpResponse:
    """Every customer with what they have spent.

    The aggregate is the query worth having in an example: it crosses the
    foreign key, so it is the one that returns nonsense if the two sides of the
    join were masked independently.
    """
    # order_count rather than orders, and this is not a style choice. Django
    # refuses an annotation whose name collides with a field or a related
    # accessor, and `orders` is the related_name on Order.customer, so
    # annotate(orders=...) raises before any SQL is sent. The response still
    # calls the key "orders"; only the alias had to move.
    rows = (
        Customer.objects.annotate(
            order_count=Count("orders"), spent=Sum("orders__total_cents")
        )
        .order_by("id")
        .values("id", "name", "email", "order_count", "spent")[:100]
    )
    return JsonResponse(
        [
            {
                "id": r["id"],
                "name": r["name"],
                "email": r["email"],
                "orders": r["order_count"],
                # Sum returns None rather than 0 for a customer with no orders,
                # and a null here would be a different statement from zero.
                "spent_cents": r["spent"] or 0,
            }
            for r in rows
        ],
        safe=False,
    )


@csrf_exempt
@require_http_methods(["POST"])
def create_order(request: HttpRequest) -> HttpResponse:
    try:
        body = json.loads(request.body or b"{}")
    except json.JSONDecodeError:
        return JsonResponse({"error": "body is not JSON"}, status=400)

    customer_id = body.get("customer_id")
    total_cents = body.get("total_cents")
    # Refused rather than stored. An order with no customer is the row the
    # invariant in the manifest exists to catch, and a service that can create
    # one makes that invariant a report rather than a guarantee.
    if not isinstance(customer_id, int) or not isinstance(total_cents, int) or total_cents <= 0:
        return JsonResponse(
            {"error": "customer_id and a positive total_cents are required"}, status=422
        )
    if not Customer.objects.filter(pk=customer_id).exists():
        return JsonResponse({"error": "no such customer"}, status=422)

    order = Order.objects.create(customer_id=customer_id, total_cents=total_cents)
    return JsonResponse(
        {
            "id": order.pk,
            "customer_id": order.customer_id,
            "total_cents": order.total_cents,
            "placed_at": order.placed_at.isoformat(),
        },
        status=201,
    )
