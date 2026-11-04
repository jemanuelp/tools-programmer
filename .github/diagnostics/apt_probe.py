"""Reproduce only the pinned APT build step; retain no raw build log."""

import argparse
from collections import deque
import hashlib
import json
from pathlib import Path
import re
import resource
import subprocess
import tempfile


CAPTURE_LIMIT = 8 * 1024 * 1024
ERROR = re.compile(r"E:|W:|Err:|ERROR:|Failed to fetch|Could not|Unable to|unmet dependencies|[Cc]ertificate|Temporary failure|Hash Sum mismatch|No space left|Release file|not signed|NO_PUBKEY|EXPKEYSIG|BADSIG|\b(?:403|404|429|502|503|504)\b")


def apt_prefix(source):
    parts = source.split("\nRUN curl ", 1)
    if len(parts) != 2 or "apt-get" not in parts[0] or "\nRUN chmod " not in parts[0]:
        raise ValueError("APT stage boundary unavailable")
    prefix = parts[0] + "\n"
    if re.search(r"(?im)^\s*(?:COPY|ARG|ENV|USER|WORKDIR|SECRET)\b", prefix):
        raise ValueError("Unexpected APT prefix instruction")
    return prefix


def redact(line):
    line = re.sub(r"https?://\S+", "[url]", line)
    # Discard the full suffix: authorization schemes and quoted values may
    # contain spaces, so masking just one token would disclose the remainder.
    line = re.sub(r"(?i)\b(?:authorization|password|passwd|token|secret|api[_-]?key)\s*[:=].*$", "[credential]", line)
    line = re.sub(r"\b(?:gh[pousr]_[A-Za-z0-9_]+|github_pat_[A-Za-z0-9_]+)\b", "[credential]", line)
    line = re.sub(r"(?<!\w)(?:/[^\s,;]+|[A-Za-z]:\\[^\s,;]+)", "[path]", line)
    line = re.sub(r"\b(?:\d{1,3}\.){3}\d{1,3}\b|\b[0-9A-Fa-f]*:[0-9A-Fa-f:]+\b", "[address]", line)
    line = re.sub(r"\b[A-Za-z0-9_+/=-]{32,}\b", "[opaque]", line)
    return "".join(c for c in line if c.isprintable())[:400]


def error_excerpt(text):
    result = deque(maxlen=40)
    following = 0
    for line in text.splitlines():
        if len(line) > 8192:
            continue
        line = re.sub(r"^#\d+\s+(?:\d+(?:\.\d+)?\s+)?", "", line)
        if ERROR.search(line):
            following = 5
        elif following:
            following -= 1
        else:
            continue
        result.append(redact(line))
    return list(result)


def capture_limit():
    resource.setrlimit(resource.RLIMIT_FSIZE, (CAPTURE_LIMIT, CAPTURE_LIMIT))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    output = Path(args.output)
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    source = Path("lab/Dockerfile").read_text()
    report = {
        "scope": "APT preparation only; no toolchains or runtime scenarios",
        "source_revision": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
        "dockerfile_sha256": hashlib.sha256(source.encode()).hexdigest(),
    }
    with tempfile.TemporaryDirectory(prefix="apt-diagnostic-") as directory:
        context = Path(directory)
        dockerfile = context / "Dockerfile"
        dockerfile.write_text(apt_prefix(source))
        with tempfile.TemporaryFile() as log:
            try:
                process = subprocess.run(
                    ["docker", "build", "--no-cache", "--progress=plain", "--network=default", "-f", str(dockerfile), str(context)],
                    stdout=log, stderr=subprocess.STDOUT, timeout=480, preexec_fn=capture_limit,
                )
                report["exit_code"] = process.returncode
            except subprocess.TimeoutExpired:
                report["exit_code"] = 124
                report["timeout"] = True
            report["capture_limit_reached"] = log.tell() >= CAPTURE_LIMIT
            log.seek(0)
            report["errors"] = error_excerpt(log.read(CAPTURE_LIMIT).decode("utf-8", errors="replace"))
    (output / "apt-diagnostic.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))
    return 0 if report["exit_code"] == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
