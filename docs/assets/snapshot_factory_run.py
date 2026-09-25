"""Record a factory run from GitHub, for the README's timeline graphic.

    python3 docs/assets/snapshot_factory_run.py 1 2

reads issue #1 and pull request #2 through the gh CLI and writes
docs/assets/factory-run.json: the issue, every comment with the factory's
markers decoded, and the pull request. generate.py draws the timeline from
that file, so the graphic shows what happened on GitHub and nothing else
(D-0015).
"""
import base64
import json
import os
import re
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = "gitdek/invariant"
MARKER = re.compile(r"<!-- invariant:([A-Za-z0-9+/=]+) -->")


def gh(path):
    return json.loads(subprocess.run(["gh", "api", f"repos/{REPO}/{path}"], check=True, capture_output=True, text=True).stdout)


def main():
    issue_n, pr_n = sys.argv[1], sys.argv[2]
    issue = gh(f"issues/{issue_n}")
    comments = []
    for c in gh(f"issues/{issue_n}/comments?per_page=100"):
        m = MARKER.search(c["body"])
        comments.append({
            "at": c["created_at"], "by": c["user"]["login"], "url": c["html_url"],
            "body": MARKER.sub("", c["body"]).strip(),
            "marker": json.loads(base64.b64decode(m.group(1))) if m else None,
        })
    pr = gh(f"pulls/{pr_n}")
    run = {
        "repo": REPO,
        "issue": {"number": issue["number"], "title": issue["title"], "by": issue["user"]["login"],
                  "opened": issue["created_at"], "closed": issue["closed_at"], "url": issue["html_url"]},
        "comments": comments,
        "pr": {"number": pr["number"], "title": pr["title"], "opened": pr["created_at"], "merged": pr["merged_at"],
               "merge_commit": pr["merge_commit_sha"], "body": pr["body"], "url": pr["html_url"]},
    }
    path = os.path.join(HERE, "factory-run.json")
    with open(path, "w") as fh:
        json.dump(run, fh, indent=2)
        fh.write("\n")
    print("wrote", os.path.relpath(path, os.path.dirname(os.path.dirname(HERE))))


if __name__ == "__main__":
    main()
