// Playwright 설정. scripts/e2e.sh가 전용 앱 인스턴스(app-e2e)와 임시 DB를 준비한 뒤 실행한다.
const { defineConfig, devices } = require('@playwright/test');

const baseURL = process.env.BASE_URL || 'http://app-e2e:8080';

module.exports = defineConfig({
  testDir: './tests',
  // 식당과 대기열이 하나뿐이라 테스트끼리 순서가 섞이지 않도록 하나씩 실행한다.
  workers: 1,
  fullyParallel: false,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  retries: 0,
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  outputDir: 'test-results',
  globalSetup: require.resolve('./global-setup.js'),
  use: {
    baseURL,
    locale: 'ko-KR',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    // 데스크톱: 모든 테스트. 모바일: 화면 배치 테스트만 (기기 에뮬레이션)
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] }, testMatch: /layout\.spec\.js/ },
  ],
});
