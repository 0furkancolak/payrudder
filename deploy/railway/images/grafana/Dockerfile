# Grafana for Railway: the repo's data sources (pointed at <service>.railway.internal) and dashboards, plus Alertmanager. Build context: the repo
# root. Login: admin with GF_SECURITY_ADMIN_PASSWORD (bootstrap.sh); no anonymous access. Stateless: everything it shows is provisioned.
FROM busybox:1.37 AS cfg
COPY deploy/grafana/provisioning /provisioning
RUN sed -i -E 's#http://(prometheus|tempo|loki):#http://\1.railway.internal:#' /provisioning/datasources/datasources.yaml \
 && grep -q 'railway.internal' /provisioning/datasources/datasources.yaml \
 && printf '%s\n' '' '  - name: Alertmanager' '    uid: alertmanager' '    type: alertmanager' '    access: proxy' \
      '    url: http://alertmanager.railway.internal:9093' '    jsonData:' '      implementation: prometheus' >>/provisioning/datasources/datasources.yaml

FROM grafana/grafana:13.0.11
COPY --from=cfg /provisioning /etc/grafana/provisioning
COPY deploy/grafana/dashboards /var/lib/grafana/dashboards
