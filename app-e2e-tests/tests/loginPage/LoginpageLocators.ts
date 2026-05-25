import { Page, Locator } from "@playwright/test";

export class LoginPageLocators {
    readonly magicLinkInputField: Locator;
    readonly sendMagicLinkButton: Locator;
    readonly ldapUsernameInputField: Locator;
    readonly ldapPasswordInputField: Locator;
    readonly submitButton: Locator;
    readonly magicLinkLoginSuccessMessage: Locator;
    readonly magicLinkLoginErrorMessage: Locator;

    constructor(page: Page) {
        this.magicLinkInputField = page.locator('#magicEmail');
        this.sendMagicLinkButton = page.locator('#magic-link-submit');
        this.ldapUsernameInputField = page.locator('#credsLdapUsername');
        this.ldapPasswordInputField = page.locator('#credsLdapPassword');
        this.submitButton = page.locator('#ldap-creds-submit');
        this.magicLinkLoginSuccessMessage = page.getByText("A sign-in link has been sent to your email address. Please check your inbox or spam folder.");
        this.magicLinkLoginErrorMessage = page.getByText("You do not have permission to sign in.");
        

    }
}