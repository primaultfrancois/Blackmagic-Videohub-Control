"""Blackmagic Videohub Ethernet Protocol (TCP 9990) — parsing and state model.

The protocol is line based. Messages are "blocks": a header line ending with
a colon, zero or more body lines, then an empty line. ACK / NAK are blocks
without a colon. All indexes are 0-based on the wire.
"""

from __future__ import annotations

from dataclasses import dataclass, field

DEFAULT_PORT = 9990

# Lock states as reported by the device.
LOCK_OWNED = "O"      # locked by this client
LOCK_LOCKED = "L"     # locked by another client
LOCK_UNLOCKED = "U"
LOCK_FORCE = "F"      # command only: force unlock

# Routing kinds → protocol block names.
ROUTING_BLOCKS = {
    "video": "VIDEO OUTPUT ROUTING",
    "monitor": "VIDEO MONITORING OUTPUT ROUTING",
}
LOCK_BLOCKS = {
    "video": "VIDEO OUTPUT LOCKS",
    "monitor": "MONITORING OUTPUT LOCKS",
}
LABEL_BLOCKS = {
    "input": "INPUT LABELS",
    "output": "OUTPUT LABELS",
    "monitor": "MONITORING OUTPUT LABELS",
}


@dataclass
class Block:
    header: str
    lines: list[str] = field(default_factory=list)


class BlockParser:
    """Incremental parser: feed raw bytes, get complete blocks back."""

    def __init__(self) -> None:
        self._buf = ""

    def feed(self, data: bytes) -> list[Block]:
        self._buf += data.decode("utf-8", errors="replace").replace("\r\n", "\n")
        blocks: list[Block] = []
        while True:
            # Skip stray blank lines between blocks.
            self._buf = self._buf.lstrip("\n")
            end = self._buf.find("\n\n")
            if end < 0:
                # ACK/NAK may arrive as a lone line followed by a blank line
                # later; wait for the terminator.
                break
            raw, self._buf = self._buf[:end], self._buf[end + 2:]
            lines = raw.split("\n")
            header = lines[0].strip()
            if header.endswith(":"):
                header = header[:-1]
            blocks.append(Block(header, lines[1:]))
        return blocks


def encode_block(header: str, lines: list[str] | None = None) -> bytes:
    body = "".join(f"{line}\n" for line in (lines or []))
    return f"{header}:\n{body}\n".encode("utf-8")


def sanitize_label(label: str) -> str:
    return " ".join(str(label).replace("\r", " ").replace("\n", " ").split())


def _split_index(line: str) -> tuple[int, str] | None:
    head, _, rest = line.partition(" ")
    try:
        return int(head), rest
    except ValueError:
        return None


@dataclass
class HubState:
    protocol_version: str = ""
    device_present: str = ""
    model_name: str = ""
    friendly_name: str = ""
    unique_id: str = ""
    video_inputs: int = 0
    video_outputs: int = 0
    monitoring_outputs: int = 0
    processing_units: int = 0
    serial_ports: int = 0
    input_labels: list[str] = field(default_factory=list)
    output_labels: list[str] = field(default_factory=list)
    monitor_labels: list[str] = field(default_factory=list)
    routing: list[int] = field(default_factory=list)
    monitor_routing: list[int] = field(default_factory=list)
    locks: list[str] = field(default_factory=list)
    monitor_locks: list[str] = field(default_factory=list)
    configuration: dict[str, str] = field(default_factory=dict)
    extra: dict[str, dict[str, str]] = field(default_factory=dict)

    # --- sizing -----------------------------------------------------------
    def _resize(self) -> None:
        def fit(lst: list, n: int, fill) -> list:
            return (lst + [fill(i) for i in range(len(lst), n)])[:n]

        self.input_labels = fit(self.input_labels, self.video_inputs, lambda i: f"Input {i + 1}")
        self.output_labels = fit(self.output_labels, self.video_outputs, lambda i: f"Output {i + 1}")
        self.monitor_labels = fit(self.monitor_labels, self.monitoring_outputs, lambda i: f"Monitor {i + 1}")
        self.routing = fit(self.routing, self.video_outputs, lambda i: 0)
        self.monitor_routing = fit(self.monitor_routing, self.monitoring_outputs, lambda i: 0)
        self.locks = fit(self.locks, self.video_outputs, lambda i: LOCK_UNLOCKED)
        self.monitor_locks = fit(self.monitor_locks, self.monitoring_outputs, lambda i: LOCK_UNLOCKED)

    # --- applying blocks --------------------------------------------------
    def apply(self, block: Block) -> bool:
        """Update state from a status block. Returns True if state changed."""
        h = block.header
        if h == "PROTOCOL PREAMBLE":
            for line in block.lines:
                key, _, value = line.partition(":")
                if key.strip() == "Version":
                    self.protocol_version = value.strip()
            return True
        if h == "VIDEOHUB DEVICE":
            self._apply_device(block.lines)
            return True
        if h == "CONFIGURATION":
            for line in block.lines:
                key, _, value = line.partition(":")
                if key.strip():
                    self.configuration[key.strip()] = value.strip()
            return True

        label_target = {
            "INPUT LABELS": self.input_labels,
            "OUTPUT LABELS": self.output_labels,
            "MONITORING OUTPUT LABELS": self.monitor_labels,
        }.get(h)
        if label_target is not None:
            for line in block.lines:
                parsed = _split_index(line)
                if parsed and 0 <= parsed[0] < len(label_target):
                    label_target[parsed[0]] = parsed[1]
            return True

        route_target = {
            "VIDEO OUTPUT ROUTING": self.routing,
            "VIDEO MONITORING OUTPUT ROUTING": self.monitor_routing,
        }.get(h)
        if route_target is not None:
            for line in block.lines:
                parsed = _split_index(line)
                if not parsed:
                    continue
                try:
                    src = int(parsed[1].strip())
                except ValueError:
                    continue
                if 0 <= parsed[0] < len(route_target):
                    route_target[parsed[0]] = src
            return True

        lock_target = {
            "VIDEO OUTPUT LOCKS": self.locks,
            "MONITORING OUTPUT LOCKS": self.monitor_locks,
        }.get(h)
        if lock_target is not None:
            for line in block.lines:
                parsed = _split_index(line)
                if parsed and 0 <= parsed[0] < len(lock_target):
                    lock_target[parsed[0]] = parsed[1].strip()[:1] or LOCK_UNLOCKED
            return True

        if h in ("ACK", "NAK", "END PRELUDE", "PING"):
            return False

        # Blocks we don't model (serial ports, processing units, alarms,
        # input/output status...) are kept raw for display.
        if block.lines:
            entries = self.extra.setdefault(h, {})
            for line in block.lines:
                key, _, value = line.partition(" ")
                entries[key] = value
        return True

    def _apply_device(self, lines: list[str]) -> None:
        ints = {
            "Video inputs": "video_inputs",
            "Video outputs": "video_outputs",
            "Video monitoring outputs": "monitoring_outputs",
            "Video processing units": "processing_units",
            "Serial ports": "serial_ports",
        }
        strs = {
            "Device present": "device_present",
            "Model name": "model_name",
            "Friendly name": "friendly_name",
            "Unique ID": "unique_id",
        }
        for line in lines:
            key, _, value = line.partition(":")
            key, value = key.strip(), value.strip()
            if key in ints:
                try:
                    setattr(self, ints[key], int(value))
                except ValueError:
                    pass
            elif key in strs:
                setattr(self, strs[key], value)
        self._resize()

    def to_dict(self) -> dict:
        return {
            "protocolVersion": self.protocol_version,
            "devicePresent": self.device_present,
            "modelName": self.model_name,
            "friendlyName": self.friendly_name,
            "uniqueId": self.unique_id,
            "videoInputs": self.video_inputs,
            "videoOutputs": self.video_outputs,
            "monitoringOutputs": self.monitoring_outputs,
            "processingUnits": self.processing_units,
            "serialPorts": self.serial_ports,
            "inputLabels": list(self.input_labels),
            "outputLabels": list(self.output_labels),
            "monitorLabels": list(self.monitor_labels),
            "routing": list(self.routing),
            "monitorRouting": list(self.monitor_routing),
            "locks": list(self.locks),
            "monitorLocks": list(self.monitor_locks),
            "configuration": dict(self.configuration),
            "extra": {h: [f"{k} {v}".rstrip() for k, v in e.items()] for h, e in self.extra.items()},
        }

