import { createTransport, type Transport, type TransportOptions } from 'nodemailer';
import type SMTPTransport from 'nodemailer/lib/smtp-transport';
import type SMTPPool from 'nodemailer/lib/smtp-pool';
import type SendmailTransport from 'nodemailer/lib/sendmail-transport';
import type StreamTransport from 'nodemailer/lib/stream-transport';
import type JSONTransport from 'nodemailer/lib/json-transport';
import type SESTransport from 'nodemailer/lib/ses-transport';
type AllTransportOptions =
  | string
  | SMTPTransport
  | SMTPTransport.Options
  | SMTPPool
  | SMTPPool.Options
  | SendmailTransport
  | SendmailTransport.Options
  | StreamTransport
  | StreamTransport.Options
  | JSONTransport
  | JSONTransport.Options
  | SESTransport
  | SESTransport.Options
  | Transport<any>
  | TransportOptions;

export async function sendEmail({ toEmail, subject, html, text }: any) {
  const emailServer: AllTransportOptions = {
    host: process.env.EMAIL_SERVER_HOST,
    port: process.env.EMAIL_SERVER_PORT ? parseInt(process.env.EMAIL_SERVER_PORT) : 465,
    auth: {
      user: process.env.EMAIL_SERVER_USER,
      pass: process.env.EMAIL_SERVER_PASSWORD,
    },
  };

  const transport = createTransport(emailServer);
  const result = await transport.sendMail({
    to: toEmail,
    from: process.env.EMAIL_FROM ?? process.env.EMAIL_SERVER_USER,
    subject: subject,
    text: text,
    html: html,
  });
  const failed = result.rejected.concat(result.pending).filter(Boolean);
  if (failed.length) {
    throw new Error(`Email (${failed.join(', ')}) could not be sent`);
  }
}
