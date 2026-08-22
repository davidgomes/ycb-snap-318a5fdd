from typing import Any, Dict, List, Optional


class IncrementalResult:
    """A single raw payload from an incremental GraphQL response."""

    def __init__(
        self,
        *,
        data: Optional[Dict[str, Any]] = None,
        errors: Optional[List[Any]] = None,
        extensions: Optional[Dict[str, Any]] = None,
        incremental: Optional[List[Dict[str, Any]]] = None,
        has_next: bool = False,
    ) -> None:
        self.data = data
        self.errors = errors
        self.extensions = extensions
        self.incremental = incremental
        self.has_next = has_next
