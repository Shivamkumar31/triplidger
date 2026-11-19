const config = {
  poweredByHeader: false,
  async rewrites() {
    return [{source: '/api/:path*', destination: `${process.env.API_INTERNAL_URL || 'http://localhost:8080'}/api/:path*`}];
  },
  async headers() {
    return [{source: '/:path*',headers:[
      {key:'X-Content-Type-Options',value:'nosniff'},
      {key:'Referrer-Policy',value:'no-referrer'},
      {key:'X-Frame-Options',value:'DENY'},
      {key:'Access-Control-Expose-Headers',value:'X-Cache, Server-Timing'}
    ]}];
  }
};
export default config;
