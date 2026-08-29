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

  if (response.status === 204) return undefined as T;

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

  return (await response.json()) as T;
}
