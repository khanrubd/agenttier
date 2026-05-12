#!/usr/bin/env python3
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Basic usage example for the AgentTier Python SDK.

Before running: set ``AGENTTIER_API_KEY`` or ``AGENTTIER_TOKEN`` (or run the
Router in dev mode locally) and update ``API_URL`` below.
"""

from __future__ import annotations

from agenttier import AgentTierClient

API_URL = "http://localhost:8080"  # or https://agenttier.company.com in production


def main() -> None:
    with AgentTierClient(api_url=API_URL) as client:
        print("Authenticated as:", client.current_user())

        print("Available templates:")
        for template in client.list_templates():
            print(f"  - {template.name}: {template.description or '(no description)'}")

        sandbox = client.create_sandbox(template="general-coding", name="sdk-demo")
        print(f"Created sandbox {sandbox.id}; waiting for Running…")
        sandbox.wait_until_running()

        result = sandbox.exec("echo 'Hello from AgentTier!'")
        print(f"stdout: {result.stdout.strip()}")
        print(f"exit:   {result.exit_code}")

        sandbox.terminate()
        print("Terminated.")


if __name__ == "__main__":
    main()
