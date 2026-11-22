#!/usr/bin/env python3
"""Render reviewable configurations; never deploy or modify the host network."""
import sys
from deploy_config import main

if __name__ == '__main__':
    sys.exit(main())
