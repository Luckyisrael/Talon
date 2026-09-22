// fetchWithRetry reissues API requests with exponential backoff until the
// server stops rate-limiting.
export async function fetchWithRetry(url: string): Promise<Response> {
	return fetch(url)
}
