const { test, expect } = require('@playwright/test');

const email = process.env.E2E_EMAIL || 'bryantest@email.com';
const password = process.env.E2E_PASSWORD || 'test123';
const vaultPassword = process.env.E2E_VAULT_PASSWORD || 'test123';

async function signIn(page) {
  await page.goto('/sign-in');
  await page.locator('#email').fill(email);
  await page.locator('#password').fill(password);
  await page.locator('#signin-form').getByRole('button', { name: /sign in/i }).click();
  await expect(page).toHaveURL(/\/vaults$/);
}

test.describe('KeePass4Web server-rendered vault flow', () => {
  test('signs in, creates a vault, unlocks it, adds an entry, closes, and signs out', async ({ page }) => {
    const suffix = Date.now();
    const vaultName = `Playwright vault ${suffix}`;
    const entryTitle = `Playwright entry ${suffix}`;

    await signIn(page);
    await page.getByRole('button', { name: 'Create a new vault' }).click();
    await page.getByRole('dialog').locator('input[name="name"]').fill(vaultName);
    await page.getByRole('dialog').locator('input[name="password"]').fill(vaultPassword);
    await page.getByRole('button', { name: 'Create vault' }).click();
    await expect(page.getByText(vaultName)).toBeVisible();

    await page.getByRole('button', { name: 'Open vault' }).last().click();
    await expect(page).toHaveURL(/\/vaults\/unlock$/);
    await page.locator('input[name="password"]').fill(vaultPassword);
    await page.getByRole('button', { name: /unlock/i }).click();
    await expect(page).toHaveURL(/\/vaults\/browse$/);

    await page.getByRole('button', { name: 'New entry' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('input[name="title"]').fill(entryTitle);
    await dialog.locator('input[name="username"]').fill('playwright-user');
    await dialog.locator('input[name="password"]').fill('playwright-password');
    await dialog.locator('input[name="master_password"]').fill(vaultPassword);
    await dialog.getByRole('button', { name: 'Save entry' }).click();
    await expect(page).toHaveURL(/\/vaults\/browse$/);
    await expect(page.getByText(entryTitle)).toBeVisible();

    await page.getByRole('button', { name: /close vault/i }).click();
    await expect(page).toHaveURL(/\/vaults$/);
    await page.getByRole('button', { name: /sign out/i }).click();
    await expect(page).toHaveURL(/\/sign-in$/);
  });
});
