import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  async redirects() {
    return [
      {
        source: "/product/crowdi",
        destination: "/product/exploratory-users",
        permanent: true,
      },
    ];
  },
};

export default nextConfig;
