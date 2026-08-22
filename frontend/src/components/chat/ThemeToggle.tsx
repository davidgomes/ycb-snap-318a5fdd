import { SunIcon, MoonIcon } from "@heroicons/react/24/outline";

interface ThemeToggleProps {
  theme: "light" | "dark";
  onToggle: () => void;
}

export function ThemeToggle({ theme, onToggle }: ThemeToggleProps) {
  return (
    <button
      onClick={onToggle}
      style={{
        padding: "8px",
        borderRadius: "8px",
        background: "var(--claude-message-bg)",
        border: "1px solid var(--claude-border)",
        color: "var(--claude-text-secondary)",
        cursor: "pointer",
        transition: "all 0.15s ease",
        display: "flex",
        alignItems: "center",
        justifyContent: "center"
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.background = "var(--claude-sidebar-hover)";
        e.currentTarget.style.color = "var(--claude-text-primary)";
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.background = "var(--claude-message-bg)";
        e.currentTarget.style.color = "var(--claude-text-secondary)";
      }}
      aria-label="Toggle theme"
    >
      {theme === "light" ? (
        <MoonIcon style={{ width: "16px", height: "16px" }} />
      ) : (
        <SunIcon style={{ width: "16px", height: "16px" }} />
      )}
    </button>
  );
}
