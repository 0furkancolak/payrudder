# Mail catcher for Railway (demo and sandbox): the notifier sends here, nothing leaves the project. Build context: the repo root.
# Service variable PORT=8025 (the web UI and its /readyz health check). SMTP is 1025 on the private network. Messages are kept in memory.
FROM axllent/mailpit:v1.31.6
ENV MP_SMTP_BIND_ADDR=[::]:1025 MP_UI_BIND_ADDR=[::]:8025
