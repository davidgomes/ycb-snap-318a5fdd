import Foundation
import SwiftUI

// MARK: - Date Extensions
extension Date {
    func timeAgoDisplay() -> String {
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .abbreviated
        return formatter.localizedString(for: self, relativeTo: Date())
    }
    
    func formatted(style: DateFormatter.Style = .medium) -> String {
        let formatter = DateFormatter()
        formatter.dateStyle = style
        formatter.timeStyle = .short
        return formatter.string(from: self)
    }
}

// MARK: - String Extensions
extension String {
    func trimmed() -> String {
        return self.trimmingCharacters(in: .whitespacesAndNewlines)
    }
    
    var isNotEmpty: Bool {
        return !self.isEmpty
    }
    
    func truncated(to length: Int, trailing: String = "...") -> String {
        if self.count <= length {
            return self
        }
        return String(self.prefix(length)) + trailing
    }
    
    func extractMention() -> (agentId: String, message: String)? {
        let pattern = #"^@(\w+(?:-\w+)*)\s+(.*)$"#
        guard let regex = try? NSRegularExpression(pattern: pattern, options: []) else {
            return nil
        }
        
        let range = NSRange(location: 0, length: self.utf16.count)
        guard let match = regex.firstMatch(in: self, options: [], range: range) else {
            return nil
        }
        
        guard let agentIdRange = Range(match.range(at: 1), in: self),
              let messageRange = Range(match.range(at: 2), in: self) else {
            return nil
        }
        
        let agentId = String(self[agentIdRange])
        let message = String(self[messageRange])
        
        return (agentId: agentId, message: message)
    }
}

// MARK: - View Extensions
extension View {
    func hapticFeedback(_ style: Constants.HapticFeedback) -> some View {
        self.onTapGesture {
            style.trigger()
        }
    }
    
    func cornerRadius(_ radius: CGFloat, corners: UIRectCorner) -> some View {
        clipShape(RoundedCorner(radius: radius, corners: corners))
    }
    
    func hideKeyboard() {
        UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
    }
}

// MARK: - Custom Shapes
struct RoundedCorner: Shape {
    var radius: CGFloat = .infinity
    var corners: UIRectCorner = .allCorners

    func path(in rect: CGRect) -> Path {
        let path = UIBezierPath(
            roundedRect: rect,
            byRoundingCorners: corners,
            cornerRadii: CGSize(width: radius, height: radius)
        )
        return Path(path.cgPath)
    }
}

// MARK: - Color Extensions
extension Color {
    static let chatBackground = Color(UIColor.systemGroupedBackground)
    static let messageBackground = Color(UIColor.secondarySystemGroupedBackground)
    static let cardBackground = Color(UIColor.systemBackground)
    
    init(hex: String) {
        let hex = hex.trimmingCharacters(in: CharacterSet.alphanumerics.inverted)
        var int: UInt64 = 0
        Scanner(string: hex).scanHexInt64(&int)
        let a, r, g, b: UInt64
        switch hex.count {
        case 3: // RGB (12-bit)
            (a, r, g, b) = (255, (int >> 8) * 17, (int >> 4 & 0xF) * 17, (int & 0xF) * 17)
        case 6: // RGB (24-bit)
            (a, r, g, b) = (255, int >> 16, int >> 8 & 0xFF, int & 0xFF)
        case 8: // ARGB (32-bit)
            (a, r, g, b) = (int >> 24, int >> 16 & 0xFF, int >> 8 & 0xFF, int & 0xFF)
        default:
            (a, r, g, b) = (1, 1, 1, 0)
        }

        self.init(
            .sRGB,
            red: Double(r) / 255,
            green: Double(g) / 255,
            blue:  Double(b) / 255,
            opacity: Double(a) / 255
        )
    }
}

// MARK: - URL Extensions
extension URL {
    static func documentsDirectory() -> URL {
        FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
    }
    
    static func cachesDirectory() -> URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
    }
}

// MARK: - Array Extensions
extension Array where Element == ChatMessage {
    func sortedByTimestamp() -> [ChatMessage] {
        return self.sorted { $0.timestamp < $1.timestamp }
    }
    
    func groupedByAgent() -> [String: [ChatMessage]] {
        return Dictionary(grouping: self) { $0.agentId ?? "unknown" }
    }
}

extension Array where Element == Agent {
    var orchestrator: Agent? {
        return self.first { $0.isOrchestrator && $0.isEnabled }
    }
    
    var workers: [Agent] {
        return self.filter { !$0.isOrchestrator && $0.isEnabled }
    }
}

// MARK: - Optional Extensions
extension Optional where Wrapped == String {
    var orEmpty: String {
        return self ?? ""
    }
}

// MARK: - Publisher Extensions
import Combine

extension Publisher {
    func sinkToResult(_ result: @escaping (Result<Output, Failure>) -> Void) -> AnyCancellable {
        return sink(
            receiveCompletion: { completion in
                switch completion {
                case .failure(let error):
                    result(.failure(error))
                case .finished:
                    break
                }
            },
            receiveValue: { value in
                result(.success(value))
            }
        )
    }
}