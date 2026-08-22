#!/bin/bash
# Create app icon from source image
# Usage: ./create-icon.sh source-image.png

if [ $# -eq 0 ]; then
    echo "Usage: $0 <source-image.png>"
    echo "Source image should be at least 1024x1024 pixels"
    exit 1
fi

SOURCE=$1
ICONSET="icon.iconset"

if [ ! -f "$SOURCE" ]; then
    echo "Error: Source image '$SOURCE' not found"
    exit 1
fi

# Create iconset directory
mkdir -p "$ICONSET"

# Generate different sizes
sips -z 16 16 "$SOURCE" --out "${ICONSET}/icon_16x16.png"
sips -z 32 32 "$SOURCE" --out "${ICONSET}/icon_16x16@2x.png"
sips -z 32 32 "$SOURCE" --out "${ICONSET}/icon_32x32.png"
sips -z 64 64 "$SOURCE" --out "${ICONSET}/icon_32x32@2x.png"
sips -z 128 128 "$SOURCE" --out "${ICONSET}/icon_128x128.png"
sips -z 256 256 "$SOURCE" --out "${ICONSET}/icon_128x128@2x.png"
sips -z 256 256 "$SOURCE" --out "${ICONSET}/icon_256x256.png"
sips -z 512 512 "$SOURCE" --out "${ICONSET}/icon_256x256@2x.png"
sips -z 512 512 "$SOURCE" --out "${ICONSET}/icon_512x512.png"
sips -z 1024 1024 "$SOURCE" --out "${ICONSET}/icon_512x512@2x.png"

# Create icns file
iconutil -c icns "$ICONSET" -o "icon.icns"

# Create Windows ico (requires ImageMagick)
if command -v convert &> /dev/null; then
    convert "$SOURCE" -resize 256x256 "icon.ico"
    echo "Created icon.ico for Windows"
fi

# Create Linux png
cp "$SOURCE" "icon.png"

echo "Created macOS icon.icns"
echo "Place these files in the assets/ directory:"
echo "  - icon.icns (macOS)"
echo "  - icon.ico (Windows)" 
echo "  - icon.png (Linux)"

# Cleanup
rm -rf "$ICONSET"