// RFC 5321 email regex using atext characters for the local part.
// Local part: atext atoms separated by single dots — no leading, trailing, or consecutive dots.
// Domain: dot-separated labels of alphanumerics and hyphens, with a 2+ char TLD.
export const EmailRegEx = /^[a-zA-Z\d!#$%&'*+/=?^_`{|}~-]+(?:\.[a-zA-Z\d!#$%&'*+/=?^_`{|}~-]+)*@[a-zA-Z\d-]+(?:\.[a-zA-Z\d-]+)*\.[a-zA-Z]{2,}$/;

const isAlpha = (char: string) => {
  return /^[A-Za-z]+$/i.test(char);
};

const isDigit = (char: string) => {
  return /^\d+$/i.test(char);
};

const isAlphaNumWithSpace = (char: string) => {
  return /^[a-z\d\-_\s]+$/i.test(char);
};
const validation = {
  email: {
    required: 'Please enter a email',
    validate: 'Please enter a valid email',
  },
  password: {
    required: 'Please enter a password',
    lowercase: 'Please enter one lowercase character',
    uppercase: 'Please enter one uppercase character',
    number: 'Please enter one number or digit',
    symbol: 'Please enter one symbol',
    minium: 'Please enter at least 8 characters long',
  },
  confirmpassword: {
    required: 'Please enter confirm password',
    notmatched: 'Password dosent matched',
  },
  text: {
    required: 'This field required',
    onlyAlpha: 'This field can contain only alphabets',
    alphaNum: 'This field should be alpha-numeric',
    firstLetterAlpha: 'Should start with an alphabet',
    firstLetterAlphaNum: 'Should start with an alphabet or a digit',
    minlength5: 'Name should have atleast 5 characters',
  },
  number: {
    required: 'Please enter a digit',
    length: 'Please enter only ',
    type: 'Please enter only digit ',
  },
};

export function emailValidation(value: string, handler: (arg: string) => void, options: string[]) {
  const conditions = ['required', 'validate'];
  if (!conditions.every((el) => [...options].includes(el))) {
    console.error('OPTIONS_ERROR', 'Following method not contains, available methods are', conditions);
    return false;
  }
  if (options.includes('required') && value.trim().length === 0) {
    handler(validation.email.required);
    return;
  }
  if (options.includes('validate') && value.trim().length > 0 && !EmailRegEx.test(value)) {
    handler(validation.email.validate);
    return;
  }
  return handler('');
}

export function textValidation(value: string, handlerValue: any, handler: (arg: any) => void, handlerOption: string, options: string[]) {
  const conditions = ['required'];
  if (!conditions.every((el) => [...options].includes(el))) {
    console.error('OPTIONS_ERROR', 'Following method not contains, available methods are', conditions);
    return false;
  }
  if (options.includes('required') && value.toString().trim().length === 0) {
    handler((prev: any) => ({ ...prev, [handlerOption]: validation.text.required }));
    return;
  }

  // Check only alphabets are present
  if (options.includes('onlyAlpha') && !isAlpha(value.toString().trim())) {
    handler((prev: any) => ({ ...prev, [handlerOption]: validation.text.onlyAlpha }));
    return;
  }

  // Check string cannot start with a digit
  if (options.includes('firstLetterAlpha') && !isAlpha(value[0].toString().trim())) {
    handler((prev: any) => ({ ...prev, [handlerOption]: validation.text.firstLetterAlpha }));
    return;
  }

  if (options.includes('firstLetterAlphaNum') && !isDigit(value[0].toString().trim()) && !isAlpha(value[0].toString().trim())) {
    handler((prev: any) => ({ ...prev, [handlerOption]: validation.text.firstLetterAlphaNum }));
    return;
  }
  if (options.includes('minlength5') && value.length < 5) {
    handler((prev: any) => ({ ...prev, [handlerOption]: validation.text.minlength5 }));
    return;
  }

  if (options.includes('alphaNumWithSpace') && !isAlphaNumWithSpace(value)) {
    handler((prev: any) => ({ ...prev, [handlerOption]: validation.text.alphaNum }));
    return;
  }

  return handler({ ...handlerValue, [handlerOption]: '' });
}

export function numberValidation(value: number, handler: (arg: string) => void, options: string[]) {
  const conditions = ['required'];
  //var regex = new RegExp('^[0-9\b]{6}$');
  if (!conditions.every((el) => [...options].includes(el))) {
    console.error('OPTIONS_ERROR', 'Following method not contains, available methods are', conditions);
    return false;
  }
  if (options.includes('required') && value.toString().trim().length === 0) {
    handler(validation.number.required);
    return;
  }
  if (
    options.filter((item) => item.startsWith('length-')).length > 0 &&
    value.toString().trim().length !== Number(options.filter((item) => item.startsWith('length-'))[0].split('-')[1])
  ) {
    handler(validation.number.length + Number(options.filter((item) => item.startsWith('length-'))[0].split('-')[1]) + ' digit');
    return;
  }
  if (isNaN(value)) {
    handler(validation.number.type);
    return;
  }
  return handler('');
}

const validations = {
  emailValidation: emailValidation,
  textValidation: textValidation,
  numberValidation: numberValidation,
};

export default validations;
