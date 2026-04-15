#!/bin/bash
# Test script that simulates a Y/n prompt flow.
# Use with: ./test_prompt.sh | spout
# Or:       spout run ./test_prompt.sh

echo "Starting build process..."
sleep 1
echo "Compiling module 1/3..."
sleep 0.5
echo "Compiling module 2/3..."
sleep 0.5
echo "Compiling module 3/3..."
sleep 0.5
echo ""
echo "Build complete. 3 modules compiled."
echo ""

# Y/n prompt - this is where the script blocks waiting for input.
read -p "Deploy to production? [Y/n] " answer
echo ""

if [[ "$answer" == "n" || "$answer" == "N" ]]; then
  echo "Aborted."
  exit 1
fi

echo "Deploying..."
sleep 1
echo "Uploading artifacts..."
sleep 1
echo "Restarting services..."
sleep 0.5
echo ""
echo "Deploy complete!"
