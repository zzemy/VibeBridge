declare module "noise-handshake" {
  export type NoiseKeyPair = {
    secretKey: Uint8Array;
    publicKey: Uint8Array;
  };

  export type NoiseCurve = {
    DHLEN: number;
    PKLEN: number;
    SKLEN: number;
    ALG: string;
    generateKeyPair(): NoiseKeyPair;
    dh(publicKey: Uint8Array, keyPair: NoiseKeyPair): Uint8Array;
  };

  export type NoiseOptions = {
    curve?: NoiseCurve;
    psk?: Uint8Array;
  };

  export default class NoiseHandshake {
    constructor(pattern: "XXpsk0" | "IK", initiator: boolean, staticKeypair: NoiseKeyPair, options?: NoiseOptions);
    initialise(prologue: Uint8Array, remoteStatic?: Uint8Array): void;
    send(payload?: Uint8Array): Uint8Array;
    recv(message: Uint8Array): Uint8Array;
    complete: boolean;
    tx: Uint8Array | null;
    rx: Uint8Array | null;
    rs: Uint8Array | null;
    hash: Uint8Array | null;
  }
}

declare module "noise-handshake/cipher" {
  export default class NoiseCipher {
    constructor(key: Uint8Array);
    encrypt(plaintext: Uint8Array, associatedData?: Uint8Array): Uint8Array;
    decrypt(ciphertext: Uint8Array, associatedData?: Uint8Array): Uint8Array;
  }
}
