# Legacy Python implementation (swd 5.x)

Archived after the Go rewrite reached file-level SHA-1 parity on live Workshop
downloads (2026-08-01).

| Path | Contents |
|------|----------|
| `python-src/swd/` | Former `src/swd` package |
| `python-tests/` | Former `tests/` |
| `pyproject.toml` | Hatch / pip metadata |
| `python-ci.yml` | Former GitHub Actions Python CI |

To run the old tool (not maintained):

```bash
cd legacy
pip install -e ".[dev]"
# may need to adjust package path — sources live under python-src/
```

Prefer the Go binary from the repo root: `go build -o swd.exe ./cmd/swd`.
