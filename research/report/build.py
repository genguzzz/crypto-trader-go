"""Build the self-contained research report (docs/research/alt-perps-report.html, plus the copy the
web app serves at /research/alt-perps-report.html).

  python3 research/report/build.py

Inputs: research/report/history.json (narrative: studies, strategy, experiments, limits, changelog),
research/report/results/alt-regime.json (collect.py output from research/altbt runs) and
research/report/results/robust-grid.json (the parameter plateau from research/altstudy/robust.py).
The template has no external dependencies, so the file opens offline and from any static host."""
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
OUT = os.path.join(ROOT, "docs", "research", "alt-perps-report.html")
# a copy the web app serves as a static file: https://<ctg host>/research/alt-perps-report.html
WEB = os.path.join(ROOT, "web", "public", "research", "alt-perps-report.html")


def load(name):
    return json.load(open(os.path.join(HERE, name), encoding="utf-8"))


def main():
    data = {"history": load("history.json"), "results": load("results/alt-regime.json"),
            "grid": load("results/robust-grid.json")}
    blob = json.dumps(data, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")
    html = open(os.path.join(HERE, "template.html"), encoding="utf-8").read().replace("__DATA__", blob)
    for path in (OUT, WEB):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        open(path, "w", encoding="utf-8").write(html)
        print(f"wrote {os.path.relpath(path, ROOT)} ({len(html) // 1024} KB)")


if __name__ == "__main__":
    main()
