import os
import sys

BIN_NAME = "fusion-console.exe" if os.name == "nt" else "fusion-console"


def main():
    exe = os.path.join(os.path.dirname(os.path.abspath(__file__)), "bin", BIN_NAME)
    if not os.path.exists(exe):
        sys.exit(
            "fusion-console binary missing from this wheel "
            "(install a platform wheel from PyPI, not the sdist)"
        )
    os.execv(exe, [exe] + sys.argv[1:])


if __name__ == "__main__":
    main()
