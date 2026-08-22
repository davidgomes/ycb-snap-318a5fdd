# Claude Code Web Agent - AWS SAM Deployment Makefile

# Colors for output
RED := \033[0;31m
GREEN := \033[0;32m
YELLOW := \033[1;33m
NC := \033[0m

# Default stage
STAGE ?= prod
STACK_NAME := claude-web-agent$(if $(filter-out prod,$(STAGE)),-$(STAGE),)

.PHONY: help check-deps check-aws build sam-build deploy deploy-dev deploy-prod logs delete clean

# Default target
help: ## Show this help message
	@echo "Claude Code Web Agent - AWS SAM Deployment"
	@echo ""
	@echo "Usage: make [target] [STAGE=dev|staging|prod]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  $(YELLOW)%-15s$(NC) %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check-deps: ## Check if required dependencies are installed
	@echo "$(YELLOW)🔍 Checking dependencies...$(NC)"
	@command -v sam >/dev/null 2>&1 || { \
		echo "$(RED)❌ SAM CLI is not installed. Please install it first:$(NC)"; \
		echo "$(RED)   https://docs.aws.amazon.com/serverless-application-model/latest/developerguide/serverless-sam-cli-install.html$(NC)"; \
		exit 1; \
	}
	@command -v aws >/dev/null 2>&1 || { \
		echo "$(RED)❌ AWS CLI is not installed. Please install it first.$(NC)"; \
		exit 1; \
	}
	@echo "$(GREEN)✅ Dependencies check passed$(NC)"

check-aws: check-deps ## Check AWS credentials
	@echo "$(YELLOW)🔐 Checking AWS credentials...$(NC)"
	@aws sts get-caller-identity >/dev/null 2>&1 || { \
		echo "$(RED)❌ AWS CLI is not configured. Please run 'aws configure' first.$(NC)"; \
		exit 1; \
	}
	@echo "$(GREEN)✅ AWS credentials check passed$(NC)"

build: ## Build the application
	@echo "$(YELLOW)📦 Building application...$(NC)"
	@npm run build
	@echo "$(GREEN)✅ Application built successfully$(NC)"

sam-build: build ## Build SAM application
	@echo "$(YELLOW)🏗️  Building SAM application...$(NC)"
	@sam build
	@echo "$(GREEN)✅ SAM build completed$(NC)"

deploy: check-aws sam-build ## Deploy to AWS (default: prod)
	@echo "$(YELLOW)🚀 Deploying to AWS (Stage: $(STAGE))...$(NC)"
	@if [ "$(STAGE)" = "dev" ]; then \
		sam deploy --config-env dev; \
	else \
		sam deploy --config-env default; \
	fi
	@echo "$(GREEN)✅ Deployment completed successfully!$(NC)"
	@$(MAKE) outputs

deploy-dev: ## Deploy to development environment
	@$(MAKE) deploy STAGE=dev

deploy-prod: ## Deploy to production environment  
	@$(MAKE) deploy STAGE=prod

deploy-guided: check-aws sam-build ## Deploy with guided setup (first time)
	@echo "$(YELLOW)🚀 Running guided deployment...$(NC)"
	@sam deploy --guided
	@echo "$(GREEN)✅ Guided deployment completed!$(NC)"

outputs: ## Show stack outputs
	@echo "$(YELLOW)📋 Stack outputs:$(NC)"
	@aws cloudformation describe-stacks \
		--stack-name "$(STACK_NAME)" \
		--query 'Stacks[0].Outputs' \
		--output table 2>/dev/null || echo "$(YELLOW)No outputs available$(NC)"

logs: ## Tail Lambda function logs
	@echo "$(YELLOW)📋 Tailing logs for $(STACK_NAME)...$(NC)"
	@sam logs -n ClaudeWebAgentFunction --stack-name $(STACK_NAME) --tail

delete: ## Delete the CloudFormation stack
	@echo "$(YELLOW)🗑️  Deleting stack $(STACK_NAME)...$(NC)"
	@echo "$(RED)⚠️  This will delete all AWS resources. Are you sure? (y/N)$(NC)"
	@read -r confirm && [ "$$confirm" = "y" ] || [ "$$confirm" = "Y" ] || { echo "Cancelled."; exit 1; }
	@sam delete --stack-name $(STACK_NAME)
	@echo "$(GREEN)✅ Stack deleted successfully$(NC)"

clean: ## Clean build artifacts
	@echo "$(YELLOW)🧹 Cleaning build artifacts...$(NC)"
	@rm -rf .aws-sam/ dist/
	@echo "$(GREEN)✅ Clean completed$(NC)"

status: ## Show deployment status
	@echo "$(YELLOW)📊 Deployment status for $(STACK_NAME):$(NC)"
	@aws cloudformation describe-stacks \
		--stack-name "$(STACK_NAME)" \
		--query 'Stacks[0].{Status:StackStatus,Created:CreationTime,Updated:LastUpdatedTime}' \
		--output table 2>/dev/null || echo "$(RED)Stack not found$(NC)"

# Development helpers
dev-build: ## Build for development (with watch)
	@echo "$(YELLOW)👀 Building with watch mode...$(NC)"
	@npm run dev

test: ## Run tests
	@echo "$(YELLOW)🧪 Running tests...$(NC)"
	@npm test

lint: ## Run linter
	@echo "$(YELLOW)🔍 Running linter...$(NC)"
	@npm run lint

format: ## Format code
	@echo "$(YELLOW)✨ Formatting code...$(NC)"
	@npm run format

typecheck: ## Run TypeScript type checking
	@echo "$(YELLOW)🔍 Running type check...$(NC)"
	@npm run typecheck

# Quality checks
check: lint typecheck test ## Run all quality checks

# Deployment pipeline
pipeline: check build sam-build ## Run full pipeline (check + build)
	@echo "$(GREEN)🎉 Pipeline completed successfully!$(NC)"