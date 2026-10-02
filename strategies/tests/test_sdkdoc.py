import json
import os
import unittest

from ctg.sdkdoc import dump

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


class SdkDocTest(unittest.TestCase):
    def test_web_copy_is_current(self):
        with open(os.path.join(ROOT, "web", "src", "docs", "sdk.json"), encoding="utf-8") as f:
            got = json.load(f)
        self.assertEqual(got, dump(), "regenerate: cd strategies && python3 -m ctg.sdkdoc > ../web/src/docs/sdk.json")
