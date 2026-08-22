import SwiftUI

struct AgentCardView: View {
    let agent: Agent
    let isSelected: Bool
    
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // Header with name and status
            HStack {
                HStack(spacing: 6) {
                    // Agent type icon
                    Image(systemName: agent.isOrchestrator ? Constants.Icons.orchestrator : Constants.Icons.agent)
                        .foregroundColor(agent.isOrchestrator ? Constants.Colors.accent : Constants.Colors.primary)
                        .font(.system(size: 16, weight: .medium))
                    
                    Text(agent.name)
                        .font(.headline)
                        .fontWeight(.semibold)
                        .lineLimit(1)
                }
                
                Spacer()
                
                // Status indicator
                HStack(spacing: 4) {
                    Circle()
                        .fill(agent.isEnabled ? Constants.Colors.success : Constants.Colors.secondary)
                        .frame(width: 8, height: 8)
                    
                    Text(agent.isEnabled ? "Active" : "Disabled")
                        .font(.caption2)
                        .foregroundColor(agent.isEnabled ? Constants.Colors.success : Constants.Colors.secondary)
                }
            }
            
            // Description
            Text(agent.description)
                .font(.body)
                .foregroundColor(.secondary)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
            
            // Details
            VStack(alignment: .leading, spacing: 4) {
                DetailRow(icon: "folder", text: agent.workingDirectory.truncated(to: 30))
                DetailRow(icon: "network", text: agent.apiEndpoint)
            }
            
            // Agent type badge
            if agent.isOrchestrator {
                HStack {
                    Text("Orchestrator")
                        .font(.caption)
                        .fontWeight(.medium)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(Constants.Colors.accent.opacity(0.1))
                        .foregroundColor(Constants.Colors.accent)
                        .cornerRadius(Constants.Dimensions.smallCornerRadius)
                    
                    Spacer()
                }
            }
        }
        .padding()
        .background(cardBackground)
        .overlay(
            RoundedRectangle(cornerRadius: Constants.Dimensions.cornerRadius)
                .stroke(borderColor, lineWidth: isSelected ? 2 : 1)
        )
        .cornerRadius(Constants.Dimensions.cornerRadius)
        .scaleEffect(isSelected ? 1.02 : 1.0)
        .shadow(
            color: isSelected ? Constants.Colors.primary.opacity(0.3) : Color.black.opacity(0.1),
            radius: isSelected ? 8 : 4,
            x: 0,
            y: isSelected ? 4 : 2
        )
        .animation(Constants.Animation.spring, value: isSelected)
    }
    
    private var cardBackground: Color {
        if isSelected {
            return Constants.Colors.primary.opacity(0.05)
        } else {
            return Color.cardBackground
        }
    }
    
    private var borderColor: Color {
        if isSelected {
            return Constants.Colors.primary
        } else if agent.isEnabled {
            return Color.clear
        } else {
            return Constants.Colors.secondary.opacity(0.3)
        }
    }
}

private struct DetailRow: View {
    let icon: String
    let text: String
    
    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: icon)
                .font(.caption)
                .foregroundColor(.secondary)
                .frame(width: 12)
            
            Text(text)
                .font(.caption)
                .foregroundColor(.secondary)
                .lineLimit(1)
        }
    }
}

#Preview {
    VStack(spacing: 16) {
        AgentCardView(
            agent: Agent.sampleAgents[0],
            isSelected: false
        )
        
        AgentCardView(
            agent: Agent.sampleAgents[1],
            isSelected: true
        )
        
        AgentCardView(
            agent: Agent(
                id: "disabled",
                name: "Disabled Agent",
                description: "This agent is currently disabled",
                workingDirectory: "/path/to/project",
                apiEndpoint: "http://localhost:8080",
                isEnabled: false
            ),
            isSelected: false
        )
    }
    .padding()
    .background(Color.chatBackground)
}