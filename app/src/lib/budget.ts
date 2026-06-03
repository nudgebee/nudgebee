import { getDateDiff, getEndOfMonth, getStartOfMonth } from './datetime';

export function getBudgetString(base: number, expense: number): string {
  if (!base) {
    return '-';
  }
  if (!expense) {
    return '-';
  }

  if (base == 0) {
    return '-';
  }

  if (expense > base) {
    return `Up ${(((expense - base) * 100) / base).toFixed(2)} %`;
  }

  return `Down ${(((base - expense) * 100) / base).toFixed(2)} %`;
}

export function getBudgetString1(base: number, expense: number): string {
  if (!base) {
    return '-';
  }
  if (!expense) {
    return '-';
  }

  if (base == 0) {
    return '-';
  }

  if (expense > base) {
    return `${(((expense - base) * 100) / base).toFixed(2)} %`;
  }

  return `${(((base - expense) * 100) / base).toFixed(2)} %`;
}

export function getBudgetExpectedMonthlyExpense(base: number, startDate?: Date, endDate?: Date): number {
  if (!base) {
    return 0;
  }

  if (base == 0) {
    return 0;
  }

  if (!endDate) {
    endDate = getEndOfMonth();
  }

  if (!startDate) {
    startDate = getStartOfMonth();
  }

  if (startDate.getMonth() === endDate.getMonth()) {
    endDate = new Date();
  }

  const dateDiff = getDateDiff(startDate, endDate);

  if (dateDiff.days == 0) {
    return base;
  }

  const endDateOfCurrentMonth = getEndOfMonth();

  return (base * endDateOfCurrentMonth.getDate()) / dateDiff.days;
}

export function getExpectedYearlyExpense(mtd = 0, ytd = 0): number {
  const d = new Date();
  const currentMonth = d.getMonth() + 1;
  return getBudgetExpectedMonthlyExpense(mtd) * (12 - currentMonth) + ytd;
}

export function getRecommendationExpectedYearlySaving(base: number): number {
  if (!base) {
    return 0;
  }
  if (base == 0) {
    return 0;
  }
  return base * 12;
}
