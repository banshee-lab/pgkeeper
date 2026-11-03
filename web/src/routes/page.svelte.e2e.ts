import { expect, test } from '@playwright/test';

test('has expected h1', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();
	await expect(page.getByText('Recent activity')).toBeVisible();
	await expect(page.getByText('Connecting…')).toBeVisible();
	await expect(page.getByText('billing-prod').first()).toBeVisible();
	await expect(page.locator('[aria-label="Backup statistics"]')).toContainText('11');
});

test('opens and closes the mobile navigation drawer', async ({ page }) => {
	await page.setViewportSize({ width: 375, height: 812 });
	await page.goto('/');
	await page.getByRole('button', { name: 'Open navigation' }).click();
	await expect(page.getByRole('dialog', { name: 'Primary navigation' })).toBeVisible();
	await page.getByRole('button', { name: 'Close navigation' }).click();
	await expect(page.getByRole('dialog', { name: 'Primary navigation' })).toHaveCount(0);
});
