import SwiftUI

struct ExecutionPlanView: View {
    let steps: [ExecutionStep]
    @State private var expandedSteps: Set<String> = []
    
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            // Header
            HStack {
                Image(systemName: "list.bullet.clipboard")
                    .foregroundColor(Constants.Colors.accent)
                Text("Execution Plan")
                    .font(.headline)
                    .fontWeight(.semibold)
                Spacer()
                Text("\(steps.count) steps")
                    .font(.caption)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Constants.Colors.accent.opacity(0.1))
                    .foregroundColor(Constants.Colors.accent)
                    .cornerRadius(Constants.Dimensions.smallCornerRadius)
            }
            
            // Steps
            VStack(alignment: .leading, spacing: 8) {
                ForEach(Array(steps.enumerated()), id: \.element.id) { index, step in
                    ExecutionStepView(
                        step: step,
                        stepNumber: index + 1,
                        isExpanded: expandedSteps.contains(step.id),
                        onToggleExpanded: {
                            toggleStepExpansion(stepId: step.id)
                        }
                    )
                }
            }
            
            // Summary
            HStack {
                StatusSummaryView(steps: steps)
                Spacer()
            }
        }
        .padding()
        .background(Constants.Colors.accent.opacity(0.05))
        .cornerRadius(Constants.Dimensions.cornerRadius)
        .overlay(
            RoundedRectangle(cornerRadius: Constants.Dimensions.cornerRadius)
                .stroke(Constants.Colors.accent.opacity(0.2), lineWidth: 1)
        )
    }
    
    private func toggleStepExpansion(stepId: String) {
        if expandedSteps.contains(stepId) {
            expandedSteps.remove(stepId)
        } else {
            expandedSteps.insert(stepId)
        }
    }
}

private struct ExecutionStepView: View {
    let step: ExecutionStep
    let stepNumber: Int
    let isExpanded: Bool
    let onToggleExpanded: () -> Void
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // Main row
            HStack(spacing: 12) {
                // Step number and status
                ZStack {
                    Circle()
                        .fill(statusBackgroundColor)
                        .frame(width: 28, height: 28)
                    
                    if step.status == .completed {
                        Image(systemName: "checkmark")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundColor(.white)
                    } else if step.status == .failed {
                        Image(systemName: "xmark")
                            .font(.system(size: 12, weight: .bold))
                            .foregroundColor(.white)
                    } else if step.status == .running {
                        ProgressView()
                            .scaleEffect(0.7)
                            .tint(.white)
                    } else {
                        Text("\(stepNumber)")
                            .font(.caption)
                            .fontWeight(.bold)
                            .foregroundColor(statusTextColor)
                    }
                }
                
                // Step content
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(step.agent)
                            .font(.caption)
                            .fontWeight(.medium)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Constants.Colors.primary.opacity(0.1))
                            .foregroundColor(Constants.Colors.primary)
                            .cornerRadius(4)
                        
                        Spacer()
                        
                        Text(step.status.rawValue.capitalized)
                            .font(.caption2)
                            .foregroundColor(statusTextColor)
                    }
                    
                    Text(step.message)
                        .font(.body)
                        .lineLimit(isExpanded ? nil : 2)
                        .animation(.easeInOut(duration: 0.2), value: isExpanded)
                }
                
                // Expand button
                Button(action: onToggleExpanded) {
                    Image(systemName: isExpanded ? "chevron.up" : "chevron.down")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
            }
            
            // Dependencies (when expanded)
            if isExpanded && !(step.dependencies?.isEmpty ?? true) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Dependencies:")
                        .font(.caption)
                        .fontWeight(.medium)
                        .foregroundColor(.secondary)
                    
                    HStack {
                        ForEach(step.dependencies ?? [], id: \.self) { dependency in
                            Text("Step \(dependency)")
                                .font(.caption2)
                                .padding(.horizontal, 6)
                                .padding(.vertical, 2)
                                .background(Constants.Colors.secondary.opacity(0.1))
                                .foregroundColor(.secondary)
                                .cornerRadius(4)
                        }
                    }
                }
                .padding(.leading, 40)
            }
        }
        .padding(.vertical, 8)
        .padding(.horizontal, 12)
        .background(stepBackgroundColor)
        .cornerRadius(Constants.Dimensions.smallCornerRadius)
        .onTapGesture {
            onToggleExpanded()
        }
    }
    
    private var statusBackgroundColor: Color {
        switch step.status {
        case .pending:
            return Constants.Colors.secondary
        case .running:
            return Constants.Colors.primary
        case .completed:
            return Constants.Colors.success
        case .failed:
            return Constants.Colors.error
        }
    }
    
    private var statusTextColor: Color {
        switch step.status {
        case .pending:
            return Constants.Colors.secondary
        case .running:
            return Constants.Colors.primary
        case .completed:
            return Constants.Colors.success
        case .failed:
            return Constants.Colors.error
        }
    }
    
    private var stepBackgroundColor: Color {
        switch step.status {
        case .pending:
            return Color.clear
        case .running:
            return Constants.Colors.primary.opacity(0.05)
        case .completed:
            return Constants.Colors.success.opacity(0.05)
        case .failed:
            return Constants.Colors.error.opacity(0.05)
        }
    }
}

private struct StatusSummaryView: View {
    let steps: [ExecutionStep]
    
    private var statusCounts: [ExecutionStep.ExecutionStatus: Int] {
        Dictionary(grouping: steps, by: { $0.status })
            .mapValues { $0.count }
    }
    
    var body: some View {
        HStack(spacing: 16) {
            if let completed = statusCounts[.completed], completed > 0 {
                StatusBadge(
                    count: completed,
                    status: .completed,
                    icon: "checkmark.circle.fill",
                    color: Constants.Colors.success
                )
            }
            
            if let running = statusCounts[.running], running > 0 {
                StatusBadge(
                    count: running,
                    status: .running,
                    icon: "clock.fill",
                    color: Constants.Colors.primary
                )
            }
            
            if let pending = statusCounts[.pending], pending > 0 {
                StatusBadge(
                    count: pending,
                    status: .pending,
                    icon: "circle",
                    color: Constants.Colors.secondary
                )
            }
            
            if let failed = statusCounts[.failed], failed > 0 {
                StatusBadge(
                    count: failed,
                    status: .failed,
                    icon: "xmark.circle.fill",
                    color: Constants.Colors.error
                )
            }
        }
    }
}

private struct StatusBadge: View {
    let count: Int
    let status: ExecutionStep.ExecutionStatus
    let icon: String
    let color: Color
    
    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: icon)
                .font(.caption2)
            Text("\(count)")
                .font(.caption2)
                .fontWeight(.medium)
        }
        .foregroundColor(color)
        .padding(.horizontal, 6)
        .padding(.vertical, 3)
        .background(color.opacity(0.1))
        .cornerRadius(Constants.Dimensions.smallCornerRadius)
    }
}

#Preview {
    VStack(spacing: 20) {
        ExecutionPlanView(steps: [
            ExecutionStep(id: "1", agent: "api-agent", message: "Create user model and authentication endpoints", status: .completed),
            ExecutionStep(id: "2", agent: "frontend-agent", message: "Create login and registration forms with proper validation", dependencies: ["1"], status: .running),
            ExecutionStep(id: "3", agent: "database-agent", message: "Set up database schema and migrations", status: .pending),
            ExecutionStep(id: "4", agent: "test-agent", message: "Write comprehensive tests for authentication flow", dependencies: ["1", "2"], status: .pending)
        ])
    }
    .padding()
    .background(Color.chatBackground)
}