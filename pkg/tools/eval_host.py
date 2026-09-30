import ast
import contextlib
import io
import json
import sys
import traceback


def emit(frame_id, ok, out, value, error):
    payload = json.dumps({"ok": ok, "stdout": out, "value": value, "error": error}, ensure_ascii=False)
    raw = payload.encode("utf-8")
    stream = sys.stdout.buffer
    stream.write(("%s %d\n" % (frame_id, len(raw))).encode("ascii"))
    stream.write(raw)
    stream.flush()


def read_exactly(stream, size):
    chunks = []
    remaining = size
    while remaining > 0:
        chunk = stream.read(remaining)
        if not chunk:
            return None
        chunks.append(chunk)
        remaining -= len(chunk)
    return b"".join(chunks)


def run(code):
    out = io.StringIO()
    value = ""
    try:
        tree = ast.parse(code)
        body = tree.body
        with contextlib.redirect_stdout(out):
            if body and isinstance(body[-1], ast.Expr):
                module = ast.Module(body=body[:-1], type_ignores=[])
                exec(compile(module, "<eval>", "exec"), STATE)
                expr = compile(ast.Expression(body=body[-1].value), "<eval>", "eval")
                result = eval(expr, STATE)
                if result is not None:
                    value = repr(result)
            else:
                exec(compile(tree, "<eval>", "exec"), STATE)
    except BaseException:
        return False, out.getvalue(), "", traceback.format_exc()
    return True, out.getvalue(), value, ""


STATE = {"__name__": "__main__", "__builtins__": __builtins__}


def main():
    stream = sys.stdin.buffer
    while True:
        header = stream.readline()
        if not header:
            break
        parts = header.decode("utf-8", "replace").split()
        if len(parts) != 2:
            continue
        try:
            size = int(parts[1])
        except ValueError:
            continue
        data = read_exactly(stream, size)
        if data is None:
            break
        ok, out, value, error = run(data.decode("utf-8"))
        emit(parts[0], ok, out, value, error)


if __name__ == "__main__":
    main()
