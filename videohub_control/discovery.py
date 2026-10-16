"""Minimal mDNS browser for Blackmagic devices (_blackmagic._tcp.local)."""

from __future__ import annotations

import select
import socket
import struct
import time

MDNS_ADDR = ("224.0.0.251", 5353)
SERVICES = ("_blackmagic._tcp.local", "_videohub._tcp.local")
T_A, T_PTR, T_TXT, T_SRV = 1, 12, 16, 33


def _encode_name(name: str) -> bytes:
    out = b""
    for part in name.strip(".").split("."):
        raw = part.encode("utf-8")
        out += bytes([len(raw)]) + raw
    return out + b"\0"


def _build_query() -> bytes:
    header = struct.pack("!HHHHHH", 0, 0, len(SERVICES), 0, 0, 0)
    # QU bit (0x8000) asks responders to answer us directly by unicast.
    questions = b"".join(_encode_name(s) + struct.pack("!HH", T_PTR, 0x8001) for s in SERVICES)
    return header + questions


def _read_name(data: bytes, offset: int) -> tuple[str, int]:
    labels: list[str] = []
    end = None
    for _ in range(128):  # loop guard against malicious pointers
        length = data[offset]
        if length == 0:
            offset += 1
            break
        if length & 0xC0 == 0xC0:
            if end is None:
                end = offset + 2
            offset = ((length & 0x3F) << 8) | data[offset + 1]
            continue
        labels.append(data[offset + 1: offset + 1 + length].decode("utf-8", "replace"))
        offset += 1 + length
    return ".".join(labels), (end if end is not None else offset)


def _parse(data: bytes, records: dict) -> None:
    _, flags, qd, an, ns, ar = struct.unpack_from("!HHHHHH", data)
    if not flags & 0x8000:
        return  # a query, not a response
    off = 12
    for _ in range(qd):
        _, off = _read_name(data, off)
        off += 4
    for _ in range(an + ns + ar):
        name, off = _read_name(data, off)
        rtype, _, _, rdlen = struct.unpack_from("!HHIH", data, off)
        off += 10
        rdata_off, off = off, off + rdlen
        if rtype == T_PTR:
            target, _ = _read_name(data, rdata_off)
            records["ptr"].setdefault(name.lower(), set()).add(target)
        elif rtype == T_SRV:
            _, _, port = struct.unpack_from("!HHH", data, rdata_off)
            target, _ = _read_name(data, rdata_off + 6)
            records["srv"][name.lower()] = (target, port)
        elif rtype == T_TXT:
            txt, p = {}, rdata_off
            while p < rdata_off + rdlen:
                ln = data[p]
                key, _, value = data[p + 1:p + 1 + ln].decode("utf-8", "replace").partition("=")
                txt[key.lower()] = value
                p += 1 + ln
            records["txt"][name.lower()] = txt
        elif rtype == T_A and rdlen == 4:
            records["a"][name.lower()] = socket.inet_ntoa(data[rdata_off:rdata_off + 4])


def discover(timeout: float = 2.0) -> list[dict]:
    """Browse the LAN and return [{name, host, model, ...}]. Best effort."""
    records: dict = {"ptr": {}, "srv": {}, "txt": {}, "a": {}}
    sources: dict[str, str] = {}  # instance → sender IP fallback
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM, socket.IPPROTO_UDP)
    try:
        sock.setsockopt(socket.IPPROTO_IP, socket.IP_MULTICAST_TTL, 255)
        sock.bind(("", 0))
        query = _build_query()
        sock.sendto(query, MDNS_ADDR)
        deadline = time.monotonic() + timeout
        resent = False
        while (left := deadline - time.monotonic()) > 0:
            if not resent and left < timeout / 2:
                sock.sendto(query, MDNS_ADDR)
                resent = True
            ready, _, _ = select.select([sock], [], [], min(left, 0.25))
            if not ready:
                continue
            data, (addr, _) = sock.recvfrom(9000)
            try:
                before = {n for s in records["ptr"].values() for n in s}
                _parse(data, records)
                for inst in {n for s in records["ptr"].values() for n in s} - before:
                    sources.setdefault(inst.lower(), addr)
            except (struct.error, IndexError):
                continue
    except OSError:
        return []
    finally:
        sock.close()

    found: dict[str, dict] = {}
    for service in SERVICES:
        for instance in records["ptr"].get(service, ()):
            key = instance.lower()
            txt = records["txt"].get(key, {})
            target, _ = records["srv"].get(key, ("", 0))
            host = records["a"].get(target.lower()) or sources.get(key)
            if not host or host in found:
                continue
            kind = txt.get("class", "")
            if kind and "videohub" not in kind.lower():
                continue  # another Blackmagic product (ATEM, HyperDeck...)
            found[host] = {
                "name": txt.get("name") or instance.split("._")[0],
                "host": host,
                "model": txt.get("model") or txt.get("name") or "",
                "uniqueId": txt.get("unique id", ""),
            }
    return sorted(found.values(), key=lambda d: d["name"].lower())
