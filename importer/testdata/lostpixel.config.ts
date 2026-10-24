import { CustomProjectConfig } from 'lost-pixel';

// pages: ['/not-this-one'] is only a comment.
export const config: CustomProjectConfig = {
  pageShots: {
    pages: [
      { path: '/app', name: 'app' },
      { path: "/next-app", name: 'next-app', breakpoints: [640] },
      { path: `/blog/${slug}`, name: 'post' },
      { path: 'pricing', name: 'pricing', mask: [{ selector: '.price[data-live]' }] },
      { path: '/app', name: 'app-again' },
      { path: routes.account, name: 'account' },
    ],
    // IP should be localhost when running locally & 172.17.0.1 when running in GitHub action
    baseUrl: 'http://172.17.0.1:3000',
    breakpoints: [640, 1024],
  },
  lostPixelProjectId: 'xxxx',
  apiKey: process.env.LOST_PIXEL_API_KEY,
};
