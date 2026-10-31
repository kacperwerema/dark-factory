// memory-embed-macos.swift: a local embedder for issue-cli's shared memory
// (docs/memory.md, Semantic search) over the sentence embedding macOS
// ships (NaturalLanguage NLEmbedding, on-device; nothing is downloaded or
// sent). It reads Ollama's /api/embed request on stdin,
// {"model": "...", "input": ["text", ...]}, and prints
// {"embeddings": [[...], ...]}. The model name is ignored.
//
//   memory:
//     embedder:
//       command: ["swift", "scripts/memory-embed-macos.swift"]
//       model: "nl-sentence-en"
//
// Compile once for a faster start: swiftc -O scripts/memory-embed-macos.swift -o ~/.local/bin/memory-embed-macos
import Foundation
import NaturalLanguage

struct Request: Decodable { let input: [String] }
struct Answer: Encodable { let embeddings: [[Double]] }

func fail(_ msg: String) -> Never {
    FileHandle.standardError.write((msg + "\n").data(using: .utf8)!)
    exit(1)
}

let data = FileHandle.standardInput.readDataToEndOfFile()
guard let req = try? JSONDecoder().decode(Request.self, from: data) else { fail("stdin is not {\"input\": [...]}") }
guard let emb = NLEmbedding.sentenceEmbedding(for: .english) else { fail("no English sentence embedding on this Mac") }
var out: [[Double]] = []
for text in req.input {
    // A text the model can't embed gets a zero vector: cosine 0, ranked by words only.
    out.append(emb.vector(for: text) ?? [Double](repeating: 0, count: emb.dimension))
}
FileHandle.standardOutput.write(try! JSONEncoder().encode(Answer(embeddings: out)))
