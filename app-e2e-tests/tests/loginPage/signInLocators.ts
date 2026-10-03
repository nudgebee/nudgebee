// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Sign-in surface — app/src/pages/signin.tsx and app/src/pages/no-tenant-access.tsx.
//
// `grep -c data-testid app/src/pages/signin.tsx` returns 1, and that one hit is the
// `sso-<provider>-btn` template on the OAuth buttons (signin.tsx:651) — nothing this
// class locates. no-tenant-access.tsx and AuthTemplateV2.tsx return 0. So every
// locator below sits on ladder rung 2 (getByRole) where the element has an
// accessible name, and rung 3 (id) only where it does not.
//
// The text fields are ds/Input (app/src/components/common/ds/Input.tsx), which renders
// `<label htmlFor={id}>` beside `<input id={id}>` (Input.tsx:431 / 295) — that label is
// what gives them an accessible name. Verified against a replica of that DOM:
// Playwright resolves `input[type=password]` as role `textbox`, which is why the
// password field is reachable by role at all and why pages/LoginPage.ts can do the same.
//
// Field errors: ds/Input derives `id={inputId}-error` for its `role="alert"` span
// (Input.tsx:154, 460) and renders it as a sibling of that one input. The id is
// therefore scoped to its own field by construction. A bare getByRole("alert") is not —
// it matched 2 nodes on the replica when both fields were in error.
export class SignInLocators extends CommonLocators {
  readonly welcomeHeading: Locator;
  readonly magicLinkOption: Locator;
  readonly ldapOption: Locator;

  readonly ldapHeading: Locator;
  readonly ldapUsername: Locator;
  readonly ldapPassword: Locator;
  readonly ldapUsernameError: Locator;
  readonly ldapPasswordError: Locator;
  readonly signInSubmit: Locator;
  readonly backToOptions: Locator;

  readonly magicLinkHeading: Locator;
  readonly magicEmail: Locator;
  readonly magicEmailError: Locator;
  readonly magicLinkSubmit: Locator;

  readonly accessDeniedCode: Locator;
  readonly accessDeniedHeading: Locator;
  readonly accessDeniedMessage: Locator;
  readonly accessDeniedDefaultMessage: Locator;
  readonly tryAgainBtn: Locator;

  constructor(page: Page) {
    super(page);

    // Only copy that identifies the main view (signin.tsx:633). Typography renders a
    // <p>, so a getByRole("heading") fallback would match nothing — left without one
    // rather than carrying a fallback that can never fire.
    this.welcomeHeading = page.getByText("Hey! Welcome back", { exact: true });

    // AuthMethodButton stacks its title and subtitle inside the <button>, so the
    // accessible name is "<title><subtitle>" — hence the loose regex. The getByText
    // fallback lands on the inner title node, which is inside the same button, and is
    // the form pages/LoginPage.ts already drives in every run.
    this.magicLinkOption = page
      .getByRole("button", { name: /Login via Magic Link/i })
      .or(page.getByText("Login via Magic Link", { exact: false }))
      .first();
    this.ldapOption = page
      .getByRole("button", { name: /Login via LDAP/i })
      .or(page.getByText("Login via LDAP", { exact: false }))
      .first();

    this.ldapHeading = page.getByText("Sign in with LDAP", { exact: true });
    this.ldapUsername = page.getByRole("textbox", { name: "LDAP Username" }).or(page.locator("#ldapUsername")).first();
    this.ldapPassword = page.getByRole("textbox", { name: "LDAP Password" }).or(page.locator("#ldapPassword")).first();
    // Id-only on purpose: the id is generated from the field's own id, so it cannot
    // drift onto another field, and the only wider match — getByRole("alert") — is
    // ambiguous the moment both fields are in error.
    this.ldapUsernameError = page.locator("#ldapUsername-error");
    this.ldapPasswordError = page.locator("#ldapPassword-error");

    // Both the LDAP view and the Admin view render a "Sign in" button, but signin.tsx
    // mounts exactly one view at a time (signin.tsx:858-861), so this stays unique.
    this.signInSubmit = page.getByRole("button", { name: /^Sign in$/ }).first();
    this.backToOptions = page
      .getByRole("button", { name: /Back to Sign in options/i })
      .or(page.getByText("Back to Sign in options", { exact: true }))
      .first();

    this.magicLinkHeading = page.getByText("Sign in with Magic Link", { exact: true });
    this.magicEmail = page.getByRole("textbox", { name: "Email address" }).or(page.locator("#magicEmail")).first();
    // Id-only for the same reason as the LDAP field errors above.
    this.magicEmailError = page.locator("#magicEmail-error");
    // The label flips to "Sending..." while the request is in flight (signin.tsx:419),
    // so the name has to admit both spellings or the locator goes stale mid-click.
    this.magicLinkSubmit = page
      .getByRole("button", { name: /get magic link|sending/i })
      .or(page.locator("#magic-link-submit"))
      .first();

    // no-tenant-access.tsx renders plain <h1>/<p> with no id or testid.
    //
    // By role, not by text: after a client-side navigation Next.js mirrors the heading
    // into its route announcer (`<p role="alert" id="__next-route-announcer__">403</p>`),
    // so getByText("403") matches twice and fails on strict mode. The announcer is a
    // <p role="alert">, never a heading, so asking for the heading excludes it — and it
    // keeps matching on a direct load, where no announcer is populated at all.
    this.accessDeniedCode = page
      .getByRole("heading", { name: "403", exact: true })
      .or(page.locator("h1").filter({ hasText: /^403$/ }))
      .first();
    this.accessDeniedHeading = page.getByText("Access Denied", { exact: true });
    // Two different bodies: the message signin.tsx passes on the NO_TENANT_ACCESS
    // redirect (signin.tsx:154), and the page's own copy when it is opened with no
    // `message` query param (no-tenant-access.tsx:74).
    this.accessDeniedMessage = page.getByText(/do not have a tenant assigned/i);
    this.accessDeniedDefaultMessage = page.getByText(/do not have an account or tenant access/i);
    this.tryAgainBtn = page.getByRole("button", { name: "Try Again" }).first();
  }
}
