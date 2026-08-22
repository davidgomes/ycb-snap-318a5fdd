# AWS Deployment Guide

This guide explains how to deploy the Claude Code Web Agent backend to AWS using SAM (Serverless Application Model).

## Prerequisites

1. **AWS CLI**: Install and configure with your AWS credentials
   ```bash
   aws configure
   ```

2. **SAM CLI**: Install the AWS SAM CLI
   ```bash
   # macOS
   brew install aws-sam-cli
   
   # Or follow the official guide:
   # https://docs.aws.amazon.com/serverless-application-model/latest/developerguide/serverless-sam-cli-install.html
   ```

3. **Node.js**: Version 20.0.0 or higher

4. **Claude CLI**: The Claude CLI must be available in your Lambda environment
   - Consider creating a Lambda layer with the Claude CLI binary
   - Or use AWS Lambda extensions

## Deployment

### Quick Deployment

Use the provided Makefile:

```bash
# Deploy to production
make deploy

# Deploy to development  
make deploy-dev

# First time deployment (guided setup)
make deploy-guided
```

### Available Make Targets

```bash
# Show all available commands
make help

# Check dependencies and AWS credentials
make check-aws

# Build and deploy pipeline
make pipeline && make deploy

# View deployment status and outputs
make status
make outputs

# Monitor logs
make logs

# Clean up resources
make delete
```

### Manual Deployment Steps

1. **Check dependencies**:
   ```bash
   make check-deps
   ```

2. **Build the application**:
   ```bash
   make build
   ```

3. **Build SAM application**:
   ```bash
   make sam-build
   ```

4. **Deploy**:
   ```bash
   make deploy STAGE=prod
   ```

## Configuration

### Environment Variables

The Lambda function uses these environment variables:

- `NODE_ENV`: Set to "production" in Lambda
- `STAGE`: Deployment stage (dev/staging/prod)

### Parameters

You can customize deployment using SAM parameters:

```bash
sam deploy --parameter-overrides Stage=dev
```

### Multiple Environments

Deploy to different environments using Make targets:

```bash
# Development
make deploy-dev

# Production  
make deploy-prod

# Custom stage
make deploy STAGE=staging
```

## Architecture

The deployment creates:

- **Lambda Function**: Runs the Hono-based API server
- **API Gateway**: Provides HTTP endpoints
- **CloudWatch Logs**: For function logging
- **IAM Roles**: With minimal required permissions

## API Endpoints

Once deployed, the API will be available at:

```
https://{api-gateway-id}.execute-api.{region}.amazonaws.com/{stage}/
```

Endpoints:
- `GET /api/projects` - List projects
- `POST /api/chat` - Chat interface
- `POST /api/abort/:requestId` - Abort requests
- `GET /api/projects/:project/histories` - List conversation histories
- `GET /api/projects/:project/histories/:sessionId` - Get conversation history

## Monitoring

View logs using:

```bash
make logs
```

## Cleanup

To remove all AWS resources:

```bash
make delete
```

## Troubleshooting

### Claude CLI Not Found

If you get "claude command not found" errors:

1. Create a Lambda layer with the Claude CLI binary
2. Use AWS Lambda extensions to download Claude CLI at runtime
3. Or include the binary in your deployment package

### Memory/Timeout Issues

Adjust Lambda configuration in `template.yml`:

```yaml
Globals:
  Function:
    Timeout: 300      # Increase timeout
    MemorySize: 1024  # Increase memory
```

### CORS Issues

CORS is configured in the API Gateway definition. Modify the `template.yml` if you need different CORS settings.

## Cost Optimization

- Lambda functions are billed per request and execution time
- API Gateway charges per API call
- CloudWatch Logs have storage costs
- Consider using Lambda reserved concurrency for cost control

## Security Considerations

- The deployment uses minimal IAM permissions
- API Gateway endpoints are public by default
- Consider adding authentication/authorization if needed
- Review CloudWatch log retention settings