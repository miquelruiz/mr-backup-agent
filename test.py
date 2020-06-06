#!/usr/bin/env python3

import argparse
import logging
import sys
import time


log = logging.getLogger(__name__)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--debug', action='store_true', default=False)
    parser.add_argument('n', type=int)
    args = parser.parse_args()

    logging.basicConfig(level=logging.DEBUG if args.debug else logging.INFO)

    logging.info("Speed in subprocess: %d", args.n)
    time.sleep(7 + (args.n/10))
    return 0

if __name__ == '__main__':
    sys.exit(main())
