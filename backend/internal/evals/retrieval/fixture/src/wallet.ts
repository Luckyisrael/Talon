// Wallet flow: authorize a transaction with the signer before broadcast.
export function authorizeTransaction(tx: string, signer: string): string {
	return signTransaction(tx, signer)
}

export function signTransaction(tx: string, signer: string): string {
	return signer + tx
}
