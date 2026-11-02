from django.urls import path

from . import views

urlpatterns = [
    path("usage", views.usage),
    path("webhooks/payrudder", views.webhook),
    path("healthz", views.healthz),
]
