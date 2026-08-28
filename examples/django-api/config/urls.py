from django.urls import path

from orders import views

urlpatterns = [
    path("health", views.health),
    path("customers", views.customers),
    path("orders", views.create_order),
]
