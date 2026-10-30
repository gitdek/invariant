"""Inventory syntax from stdin without importing or executing inspected code."""

import ast
import codecs
from collections import Counter
import io
import json
import re
import sys
import tokenize


def inventory(raw):
    encoding, _ = tokenize.detect_encoding(io.BytesIO(raw).readline)
    text = raw.decode(encoding)
    tree = ast.parse(text)

    # AST columns count UTF-8 bytes, even for sources with another encoding.
    # Map decoded character boundaries back to the original bytes, never
    # re-encode a contract or normalize its comments or newline sequences.
    offsets = [3 if raw.startswith(codecs.BOM_UTF8) else 0]
    decoder = codecs.getincrementaldecoder(encoding)()
    for index, byte in enumerate(raw):
        decoded = decoder.decode(bytes((byte,)))
        offsets.extend([index + 1] * len(decoded))
    offsets.extend([len(raw)] * len(decoder.decode(b"", final=True)))
    starts = [0] + [m.end() for m in re.finditer(r"\r\n|\r|\n", text)]

    def offset(line, column):
        start = starts[line - 1]
        end = starts[line] if line < len(starts) else len(text)
        prefix = text[start:end].encode("utf-8")[:column].decode("utf-8")
        return offsets[start + len(prefix)]

    def declarations(node):
        for child in ast.iter_child_nodes(node):
            if isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
                yield child
            else:
                yield from declarations(child)

    result = []

    def namespace(node, prefix):
        children = sorted(declarations(node), key=lambda d: (d.lineno, d.col_offset))
        counts = Counter(d.name for d in children)
        ordinals = Counter()
        for child in children:
            ordinals[child.name] += 1
            symbol = prefix + child.name
            if counts[child.name] > 1:
                symbol += "[" + str(ordinals[child.name]) + "]"
            if isinstance(child, ast.ClassDef):
                namespace(child, symbol + ".")
                continue
            contracts = []
            for statement in child.body:
                if not isinstance(statement, ast.Expr):
                    continue
                call = statement.value
                if (isinstance(call, ast.Call) and isinstance(call.func, ast.Name)
                        and call.func.id in {"Requires", "Ensures", "Exsures", "Decreases"}):
                    contracts.append([
                        offset(call.lineno, call.col_offset),
                        offset(call.end_lineno, call.end_col_offset),
                    ])
            result.append({"symbol": symbol, "contracts": contracts})
            namespace(child, symbol + ".<locals>.")

    namespace(tree, "")
    return result


try:
    print(json.dumps(inventory(sys.stdin.buffer.read())))
except (SyntaxError, UnicodeError, LookupError, ValueError, RecursionError) as error:
    sys.exit(str(error))
