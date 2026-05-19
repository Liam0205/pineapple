import threading


class CancellationToken:
    def __init__(self, parent: "CancellationToken | None" = None):
        self._cancelled = threading.Event()
        self._parent = parent

    def cancel(self):
        self._cancelled.set()

    def is_cancelled(self) -> bool:
        if self._cancelled.is_set():
            return True
        if self._parent is not None:
            return self._parent.is_cancelled()
        return False

    def child(self) -> "CancellationToken":
        return CancellationToken(parent=self)
