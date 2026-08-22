# AgentHub - Multi-Agent Programming Collaboration Tool
# Makefile for building Electron app and DMG

.PHONY: all clean install build dev electron dist dmg help

# Default target
all: build

# Help target
help:
	@echo "AgentHub Build System"
	@echo "===================="
	@echo ""
	@echo "Available targets:"
	@echo "  install            - Install all dependencies"
	@echo "  dev                - Start development servers"
	@echo "  build              - Build frontend and backend"
	@echo "  build-frontend     - Build frontend only"
	@echo "  electron           - Run Electron app (requires frontend dev server)"
	@echo "  electron-standalone- Run Electron app with built frontend"
	@echo "  dist               - Build production Electron app"
	@echo "  dmg                - Build macOS DMG installer"
	@echo "  clean              - Clean build artifacts"
	@echo "  help               - Show this help message"

# Install dependencies
install:
	@echo "Installing dependencies..."
	npm install
	cd frontend && npm install
	cd backend && npm install

# Development
dev:
	@echo "Starting development servers..."
	@echo "Backend will start on port 8080"
	@echo "Frontend will start on port 3000"
	@echo "Press Ctrl+C to stop"
	make -j2 dev-backend dev-frontend

dev-backend:
	cd backend && npm run dev

dev-frontend:
	cd frontend && npm run dev

# Run Electron in development
electron:
	@echo "Starting Electron in development mode..."
	npm run electron:dev

# Run Electron standalone (with built frontend)
electron-standalone: build-frontend
	@echo "Starting Electron with built frontend..."
	npm run electron:dev

# Build frontend only
build-frontend:
	@echo "Building frontend..."
	cd frontend && npm run build

# Build everything
build:
	@echo "Building frontend and backend..."
	npm run build:frontend
	npm run build:backend

# Build Electron app for distribution
dist: build
	@echo "Building Electron app for distribution..."
	npm run dist

# Build macOS DMG (frontend-only)
dmg: build-frontend
	@echo "Building macOS DMG installer..."
	npm run dist:mac

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf dist/
	rm -rf frontend/dist/
	rm -rf backend/dist/
	rm -rf node_modules/
	rm -rf frontend/node_modules/
	rm -rf backend/node_modules/

# Create app icon (requires iconutil on macOS)
icon:
	@echo "Creating app icon..."
	mkdir -p assets/icon.iconset
	# Add different sizes of your icon PNG files to assets/icon.iconset/
	# icon_16x16.png, icon_32x32.png, icon_128x128.png, icon_256x256.png, icon_512x512.png, icon_1024x1024.png
	# Then run: iconutil -c icns assets/icon.iconset -o assets/icon.icns

# Quick test build
test-build: clean install build

# Full release build
release: clean install build dmg
	@echo "Release build complete! Check dist/ folder for DMG file."

# Development workflow
quick-start: install
	@echo "Quick start: Installing and launching development environment..."
	make -j2 dev-backend electron

# Check system requirements
check:
	@echo "Checking system requirements..."
	@node --version || (echo "Node.js not found. Please install Node.js 18+"; exit 1)
	@npm --version || (echo "npm not found. Please install npm"; exit 1)
	@echo "System requirements OK"

# Setup development environment
setup: check install
	@echo "Development environment setup complete!"
	@echo "Run 'make dev' to start development servers"
	@echo "Run 'make electron' to start Electron app"