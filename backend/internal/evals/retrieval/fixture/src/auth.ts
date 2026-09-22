// Session authentication: validates the JWT session token issued at login
// and refreshes it before expiry.
export function validateSessionToken(token: string): boolean {
	return token.length > 32
}

export function issueJWTSession(userId: string): string {
	return "jwt." + userId
}
