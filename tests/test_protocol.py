import unittest

from videohub_control.protocol import BlockParser, HubState, encode_block, sanitize_label

PRELUDE = (
    "PROTOCOL PREAMBLE:\nVersion: 2.8\n\n"
    "VIDEOHUB DEVICE:\nDevice present: true\nModel name: Smart Videohub 12G 40x40\n"
    "Friendly name: Régie\nUnique ID: 7C2E0D021714\nVideo inputs: 4\nVideo processing units: 0\n"
    "Video outputs: 3\nVideo monitoring outputs: 1\nSerial ports: 0\n\n"
    "INPUT LABELS:\n0 Cam 1\n1 Cam 2\n2 Mac Pro\n3 Black\n\n"
    "OUTPUT LABELS:\n0 PGM\n1 Multiview\n2 Rec\n\n"
    "MONITORING OUTPUT LABELS:\n0 Check\n\n"
    "VIDEO OUTPUT LOCKS:\n0 U\n1 L\n2 O\n\n"
    "VIDEO OUTPUT ROUTING:\n0 2\n1 1\n2 0\n\n"
    "VIDEO MONITORING OUTPUT ROUTING:\n0 3\n\n"
    "VIDEO INPUT STATUS:\n0 BNC\n1 Optical\n\n"
    "CONFIGURATION:\nTake Mode: true\n\n"
    "END PRELUDE:\n\n"
)


def load(text: str) -> HubState:
    state = HubState()
    for block in BlockParser().feed(text.encode()):
        state.apply(block)
    return state


class ParserTests(unittest.TestCase):
    def test_split_across_chunks(self):
        parser = BlockParser()
        data = PRELUDE.encode()
        blocks = []
        for i in range(0, len(data), 7):
            blocks += parser.feed(data[i:i + 7])
        self.assertEqual([b.header for b in blocks][-1], "END PRELUDE")
        self.assertEqual(len(blocks), 11)

    def test_ack_nak_and_crlf(self):
        blocks = BlockParser().feed(b"ACK\r\n\r\nNAK\n\n")
        self.assertEqual([b.header for b in blocks], ["ACK", "NAK"])

    def test_encode(self):
        self.assertEqual(encode_block("VIDEO OUTPUT ROUTING", ["5 3"]), b"VIDEO OUTPUT ROUTING:\n5 3\n\n")
        self.assertEqual(encode_block("PING"), b"PING:\n\n")

    def test_sanitize_label(self):
        self.assertEqual(sanitize_label("  Cam\n 1\r "), "Cam 1")


class StateTests(unittest.TestCase):
    def test_prelude(self):
        s = load(PRELUDE)
        self.assertEqual(s.protocol_version, "2.8")
        self.assertEqual(s.model_name, "Smart Videohub 12G 40x40")
        self.assertEqual(s.friendly_name, "Régie")
        self.assertEqual((s.video_inputs, s.video_outputs, s.monitoring_outputs), (4, 3, 1))
        self.assertEqual(s.input_labels, ["Cam 1", "Cam 2", "Mac Pro", "Black"])
        self.assertEqual(s.routing, [2, 1, 0])
        self.assertEqual(s.monitor_routing, [3])
        self.assertEqual(s.locks, ["U", "L", "O"])
        self.assertEqual(s.configuration, {"Take Mode": "true"})
        self.assertEqual(s.extra["VIDEO INPUT STATUS"], {"0": "BNC", "1": "Optical"})

    def test_updates_and_bounds(self):
        s = load(PRELUDE + "VIDEO OUTPUT ROUTING:\n1 3\n99 0\nbad line\n\nOUTPUT LABELS:\n2 Rec ISO 2\n\n")
        self.assertEqual(s.routing, [2, 3, 0])
        self.assertEqual(s.output_labels[2], "Rec ISO 2")

    def test_defaults_when_labels_missing(self):
        s = load("VIDEOHUB DEVICE:\nVideo inputs: 2\nVideo outputs: 2\n\n")
        self.assertEqual(s.input_labels, ["Input 1", "Input 2"])
        self.assertEqual(s.locks, ["U", "U"])


if __name__ == "__main__":
    unittest.main()
