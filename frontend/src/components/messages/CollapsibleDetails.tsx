import { useState } from "react";

interface CollapsibleDetailsProps {
  label: string;
  details: string;
  defaultCollapsed?: boolean;
}

export function CollapsibleDetails({
  label,
  details,
  defaultCollapsed = true,
}: CollapsibleDetailsProps) {
  const [isExpanded, setIsExpanded] = useState(!defaultCollapsed);
  const hasDetails = details.trim().length > 0;

  return (
    <div className="message-item animate-in" style={{ marginBottom: '8px' }}>
      {/* System message layout */}
      <div style={{ display: 'flex', gap: '12px' }}>
        {/* Icon placeholder */}
        <div style={{ width: '28px', height: '28px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          {/* Empty space for alignment */}
        </div>
        
        {/* Collapsible content */}
        <div style={{ flex: 1, minWidth: 0 }}>
          <div
            onClick={hasDetails ? () => setIsExpanded(!isExpanded) : undefined}
            style={{
              cursor: hasDetails ? 'pointer' : 'default',
              background: 'var(--claude-message-bg)',
              border: '1px solid var(--claude-message-border)',
              borderRadius: '8px',
              padding: '6px 12px',
              marginBottom: hasDetails && isExpanded ? '0' : '8px',
              display: 'flex',
              alignItems: 'center',
              gap: '8px',
              fontSize: '13px',
              color: 'var(--claude-text-secondary)',
              borderBottomLeftRadius: hasDetails && isExpanded ? '0' : '8px',
              borderBottomRightRadius: hasDetails && isExpanded ? '0' : '8px',
            }}
          >
            <span>{label}</span>
            {hasDetails && (
              <span style={{ marginLeft: 'auto', fontSize: '10px' }}>
                {isExpanded ? "▼" : "▶"}
              </span>
            )}
          </div>
          
          {hasDetails && isExpanded && (
            <div
              style={{
                background: 'var(--claude-message-bg)',
                border: '1px solid var(--claude-message-border)',
                borderTop: 'none',
                borderBottomLeftRadius: '8px',
                borderBottomRightRadius: '8px',
                padding: '12px',
                marginBottom: '8px',
              }}
            >
              <pre
                style={{
                  fontSize: '12px',
                  fontFamily: "'SF Mono', Monaco, 'Cascadia Code', monospace",
                  color: 'var(--claude-text-primary)',
                  margin: 0,
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                  lineHeight: '1.4',
                }}
              >
                {details}
              </pre>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
