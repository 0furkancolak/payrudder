#!/usr/bin/env python3
"""Railway's GraphQL API for staging.sh: what the CLI cannot do. `railway down` removes a deployment, after which `railway service redeploy` finds
nothing to redeploy; the API redeploys the removed deployment by its id (no build), and removes a sleeping one, which `railway down` refuses.

  staging_api.py <project> <environment> states          "<service> <up|stopped|starting|failed>" per service
  staging_api.py <project> <environment> stop <svc...>   remove each service's running deployments
  staging_api.py <project> <environment> wake <svc...>   redeploy each service's newest deployment that was built

Auth: RAILWAY_TOKEN (a project token, as in CI), else RAILWAY_API_TOKEN (an account token), else the CLI's login (~/.railway/config.json).
"""
import json
import os
import subprocess
import sys
import urllib.request

URL = "https://backboard.railway.com/graphql/v2"
RUNNING = {"SUCCESS", "SLEEPING", "DEPLOYING", "INITIALIZING", "BUILDING", "QUEUED", "WAITING", "CRASHED"}
BUILT = {"SUCCESS", "SLEEPING", "REMOVED", "CRASHED"}  # a deployment with an image to redeploy; FAILED may be a failed build


def headers():
    if os.environ.get("RAILWAY_TOKEN"):
        return {"Project-Access-Token": os.environ["RAILWAY_TOKEN"]}
    token = os.environ.get("RAILWAY_API_TOKEN")
    if not token:
        # The login's access token lasts an hour; any CLI call refreshes it in the config file.
        subprocess.run(["railway", "whoami"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
        with open(os.path.expanduser("~/.railway/config.json")) as f:
            token = json.load(f)["user"]["accessToken"]
    return {"Authorization": "Bearer " + token}


def api(query, variables=None):
    req = urllib.request.Request(URL, json.dumps({"query": query, "variables": variables or {}}).encode(),
                                 {"Content-Type": "application/json", "User-Agent": "payrudder-staging", **headers()})
    with urllib.request.urlopen(req, timeout=180) as r:  # removing Postgres takes over a minute
        body = json.load(r)
    if body.get("errors"):
        sys.exit("railway api: " + "; ".join(e["message"] for e in body["errors"]))
    return body["data"]


def deployments(project, environment):
    """{service name: [deployment, newest first]} for every service of the environment, in one request."""
    p = api("query($id:String!){project(id:$id){services{edges{node{id name}}} environments{edges{node{id name}}}}}", {"id": project})["project"]
    env = next((e["node"]["id"] for e in p["environments"]["edges"] if e["node"]["name"] == environment), None) or sys.exit(f"no environment {environment}")
    services = {e["node"]["name"]: e["node"]["id"] for e in p["services"]["edges"]}
    parts = [f's{i}: deployments(first: 10, input: {{projectId: "{project}", environmentId: "{env}", serviceId: "{sid}"}}) {{edges {{node {{id status}}}}}}'
             for i, sid in enumerate(services.values())]
    data = api("{" + " ".join(parts) + "}")
    # A service with no deployment in this environment (not part of it) is left out.
    out = {name: [e["node"] for e in data[f"s{i}"]["edges"]] for i, name in enumerate(services)}
    return {name: d for name, d in out.items() if d}


def state(deps):
    s = deps[0]["status"]
    if s in ("SUCCESS", "SLEEPING"):
        return "up"
    if s == "REMOVED":
        return "stopped"
    if s in ("FAILED", "CRASHED"):
        return "failed"
    return "starting"


def main():
    project, environment, cmd, names = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4:]
    deps = deployments(project, environment)
    if cmd == "states":
        for name, d in deps.items():
            print(name, state(d))
    elif cmd == "stop":
        for name in names:
            for d in deps.get(name, []):
                if d["status"] in RUNNING:
                    api("mutation($id:String!){deploymentRemove(id:$id)}", {"id": d["id"]})
    elif cmd == "wake":
        for name in names:
            built = next((d for d in deps.get(name, []) if d["status"] in BUILT), None)
            if not built:
                sys.exit(f"{name}: no built deployment to wake (deploy it once: bootstrap.sh --deploy-only)")
            api("mutation($id:String!){deploymentRedeploy(id:$id){id}}", {"id": built["id"]})
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
