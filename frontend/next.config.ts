import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // The dev server only trusts the hostname it started on (localhost) and
  // 403s every other origin's asset/HMR requests — a LAN browser at
  // 192.168.1.6:3000 got a zombie page: HTML served, every chunk forbidden,
  // so no client JS ever ran (and no error surfaced — the page just sat
  // there). Development from any private-network address is allowed here.
  allowedDevOrigins: ["192.168.1.6", "192.168.1.*", "10.*", "172.16.*", "localhost"],
};

export default nextConfig;
