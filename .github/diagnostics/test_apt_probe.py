import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("apt_probe", Path(__file__).with_name("apt_probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class AptDiagnosticTests(unittest.TestCase):
    def test_extracts_apt_prefix_without_later_toolchain_steps(self):
        source = Path(__file__).resolve().parents[2] / "lab" / "Dockerfile"
        original = source.read_text()
        prefix = probe.apt_prefix(original)
        self.assertEqual(prefix, original.split("\nRUN curl ", 1)[0] + "\n")
        self.assertIn("apt-get", prefix)
        self.assertNotIn("go.dev", prefix)
        self.assertNotIn("COPY", prefix)

    def test_missing_boundary_fails_closed(self):
        with self.assertRaises(ValueError):
            probe.apt_prefix("FROM ubuntu\nRUN apt-get update\n")

    def test_error_excerpt_redacts_sensitive_values(self):
        raw = "#9 2.1 E: Failed to fetch https://user:password@example.test/path?token=abc IP 10.0.0.1 token=secret /home/private/file ghp_123456789012345678901234567890123456\n"
        excerpt = probe.error_excerpt(raw)
        self.assertEqual(len(excerpt), 1)
        self.assertIn("E: Failed to fetch", excerpt[0])
        for value in ["user", "password", "example.test", "abc", "10.0.0.1", "secret", "/home/private", "ghp_"]:
            self.assertNotIn(value, excerpt[0])

    def test_redacts_complete_authorization_and_quoted_password(self):
        cases = [
            ("E: Authorization: Bearer abcDEFG123456789", ["Bearer", "abcDEFG123456789"]),
            ('E: password="two words"', ["two", "words"]),
        ]
        for raw, forbidden in cases:
            with self.subTest(raw=raw):
                excerpt = "\n".join(probe.error_excerpt(raw))
                self.assertIn("[credential]", excerpt)
                for value in forbidden:
                    self.assertNotIn(value, excerpt)

    def test_excerpt_is_bounded_and_preserves_apt_details(self):
        raw = "unrelated output\n" + "\n".join(f"#9 1.0 E: Package candidate-{i} has unmet dependencies" for i in range(100))
        excerpt = probe.error_excerpt(raw)
        self.assertEqual(len(excerpt), 40)
        self.assertIn("candidate-99", excerpt[-1])
        self.assertTrue(all(len(line) <= 400 for line in excerpt))

    def test_continuation_after_error_is_retained_but_long_lines_are_omitted(self):
        raw = "#9 1.0 E: Unable to correct problems\n#9 1.1 package-a : Depends: package-b but it is not installable\n" + "x" * 9000
        self.assertEqual(len(probe.error_excerpt(raw)), 2)

    def test_public_entry_records_exit_and_only_redacted_output(self):
        for exit_code in [0, 1]:
            with self.subTest(exit_code=exit_code), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "evidence"

                def build(command, **kwargs):
                    self.assertEqual(command[:2], ["docker", "build"])
                    self.assertEqual(kwargs["timeout"], 480)
                    context = Path(command[-1])
                    self.assertEqual([p.name for p in context.iterdir()], ["Dockerfile"])
                    kwargs["stdout"].write(b"#9 1.0 E: Failed to fetch https://user:password@example.test/path\nE: Authorization: Bearer abcDEFG123456789\nE: password=\"two words\"\n")
                    return type("Result", (), {"returncode": exit_code})()

                with patch("sys.argv", ["apt_probe.py", "--output", str(output)]), patch.object(probe.subprocess, "run", side_effect=build), patch.object(probe.subprocess, "check_output", return_value="a" * 40), patch("builtins.print"):
                    self.assertEqual(probe.main(), exit_code)
                report = json.loads((output / "apt-diagnostic.json").read_text())
                self.assertEqual(report["exit_code"], exit_code)
                self.assertEqual(report["source_revision"], "a" * 40)
                self.assertEqual(report["errors"], ["E: Failed to fetch [url]", "E: [credential]", "E: [credential]"])
                self.assertEqual([p.name for p in output.iterdir()], ["apt-diagnostic.json"])


if __name__ == "__main__":
    unittest.main()
