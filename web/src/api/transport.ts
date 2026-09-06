// The only file in the frontend that mentions fetch.
//
// Everything else asks for a typed value; this is where a value stops being one and
// becomes a request. Keeping it to one file is what makes "does anything talk to a
// different origin" a question with a one-file answer.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

type Options = {
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
};

export async function request<T>(
  path: string,
  options: Options = {},
): Promise<T> {
  const { method = "GET", body, signal } = options;

  const response = await fetch(path, {
    method,
    signal,
    // Same-origin only. There is no CORS on the server side and its absence is
    // load-bearing, so a request that needed it would be a bug rather than a feature.
    credentials: "same-origin",
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  // The server answers every refusal with {"error": "a sentence"}, written for the person
  // who will read it rather than for a log grep. Showing that sentence is the whole point
  // of it existing.
  if (!response.ok) {
    let message = response.statusText;
    try {
      const body = (await response.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // A body that is not JSON means something upstream answered, not us.
    }
    throw new ApiError(response.status, message);
  }

  // Read as text and parsed here rather than through response.json(), because **not every
  // successful answer has a body**. This used to test for 204 alone, which was true until the
  // first handler answered 202 — and a 202 went to JSON.parse("") and surfaced as "unexpected
  // end of data" on a button that had in fact worked.
  //
  // A body's absence is the thing to check, not a particular status that happens to imply it.
  const text = await response.text();
  if (text === "") return undefined as T;
  return JSON.parse(text) as T;
}
