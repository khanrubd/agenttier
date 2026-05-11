#!/usr/bin/env python3
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Basic usage example for the AgentTier Python SDK."""

from agenttier import AgentTierClient

# Connect to AgentTier (uses AGENTTIER_API_KEY or AGENTTIER_TOKEN env var)
client = AgentTierClient(api_url="https://agenttier.internal.company.com")

# Create a sandbox from a template
sandbox = client.create_sandbox(
    template="general-coding",
    name="sdk-example",
    namespace="default",
)
print(f"Created sandbox: {sandbox.id}")

# Wait for it to be ready
sandbox.wait_until_running(timeout=60)
print("Sandbox is running!")

# Execute commands
result = sandbox.commands.run("echo 'Hello from AgentTier!'")
print(f"stdout: {result.stdout}")
print(f"exit code: {result.exit_code}")

# Write a file
sandbox.files.write("/workspace/hello.py", "print('Hello, world!')\n")

# Read it back
content = sandbox.files.read("/workspace/hello.py")
print(f"File content: {content.decode()}")

# Run the file
result = sandbox.commands.run("python3 /workspace/hello.py")
print(f"Python output: {result.stdout}")

# List workspace
files = sandbox.files.list("/workspace")
for f in files:
    print(f"  {'[DIR]' if f.is_dir else '     '} {f.name} ({f.size} bytes)")

# Stop (preserves files)
sandbox.stop()
print("Sandbox stopped (files preserved)")

# Resume
sandbox.resume()
sandbox.wait_until_running()
print("Sandbox resumed!")

# Verify files still exist
result = sandbox.commands.run("cat /workspace/hello.py")
print(f"File still exists: {result.stdout}")

# Clean up
sandbox.terminate()
print("Sandbox deleted")

client.close()
