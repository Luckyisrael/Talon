import { transact, Web3MobileWallet } from "@solana-mobile/mobile-wallet-adapter-protocol-web3js";
import { PublicKey } from "@solana/web3.js";
import AsyncStorage from "@react-native-async-storage/async-storage";

const AUTH_TOKEN_KEY = "@seeker/auth_token";
const WALLET_URL_KEY = "@seeker/wallet_url";

export const APP_IDENTITY = {
  name: "SeekerStudio",
  uri: "https://seekerstudio.app",
};

export async function connectWallet(): Promise<{ wallet: Web3MobileWallet; authToken: string; publicKey: PublicKey } | null> {
  try {
    const result = await transact(async (wallet: Web3MobileWallet) => {
      const storedAuthToken = await AsyncStorage.getItem(AUTH_TOKEN_KEY);

      if (storedAuthToken) {
        try {
          const reauth = await wallet.reauthorize({
            auth_token: storedAuthToken,
            identity: APP_IDENTITY,
          });
          await AsyncStorage.setItem(AUTH_TOKEN_KEY, reauth.auth_token);
          return {
            wallet,
            authToken: reauth.auth_token,
            publicKey: new PublicKey(reauth.pubkey),
          };
        } catch {
          // Silent reauth failed, fall through to full auth
        }
      }

      const auth = await wallet.authorize({
        identity: APP_IDENTITY,
      });
      await AsyncStorage.setItem(AUTH_TOKEN_KEY, auth.auth_token);
      return {
        wallet,
        authToken: auth.auth_token,
        publicKey: new PublicKey(auth.pubkey),
      };
    });

    return result;
  } catch (error) {
    console.error("Wallet connection failed:", error);
    return null;
  }
}

export async function disconnectWallet(): Promise<void> {
  await AsyncStorage.removeItem(AUTH_TOKEN_KEY);
}
