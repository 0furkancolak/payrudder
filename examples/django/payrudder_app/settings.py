import os
import secrets

# Nothing here uses sessions, cookies or signed values; Django wants a key to start. Set DJANGO_SECRET_KEY in your own app.
SECRET_KEY = os.environ.get("DJANGO_SECRET_KEY") or secrets.token_hex(32)
DEBUG = False
# Put your own host names here: this template answers on any, because the smoke test and a container behind a proxy reach it by address.
ALLOWED_HOSTS = ["*"]
ROOT_URLCONF = "payrudder_app.urls"
WSGI_APPLICATION = "payrudder_app.wsgi.application"
# The CSRF middleware stays on, as in any Django app: the two views opt out with @csrf_exempt, because a delivery and a call from your own backend carry no CSRF token.
MIDDLEWARE = ["django.middleware.common.CommonMiddleware", "django.middleware.csrf.CsrfViewMiddleware"]
INSTALLED_APPS: list[str] = []
USE_TZ = True
LOGGING = {"version": 1, "disable_existing_loggers": False, "handlers": {"console": {"class": "logging.StreamHandler"}}, "root": {"handlers": ["console"], "level": "INFO"}}
