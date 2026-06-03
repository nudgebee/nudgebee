import React, { useEffect, useState } from 'react';
import { Typography, type SxProps, type Theme } from '@mui/material';
import dayjs from 'dayjs';
import utc from 'dayjs/plugin/utc';
import timezone from 'dayjs/plugin/timezone';

// Enable dayjs timezone support
dayjs.extend(utc);
dayjs.extend(timezone);

// Constants
const IST_TIMEZONE = 'Asia/Kolkata';
const LOGIN_STORAGE_KEY_PREFIX = 'nudgebee_login_';
const LAST_SEEN_STORAGE_KEY = 'nudgebee_last_seen';
const LOGIN_DATA_RETENTION_DAYS = 7;
const FREQUENT_LOGIN_THRESHOLD = 3;

const TIME_OF_DAY = {
  MORNING_START: 5,
  AFTERNOON_START: 12,
  EVENING_START: 17,
  NIGHT_START: 21,
} as const;

interface DynamicGreetingProps {
  userName: string;
  className?: string;
  style?: React.CSSProperties;
  sx?: SxProps<Theme>;
}

interface GreetingData {
  timeOfDay: 'morning' | 'afternoon' | 'evening' | 'night';
  dayOfWeek: string;
  isMonday: boolean;
  isTuesday: boolean;
  isWednesday: boolean;
  isThursday: boolean;
  isFriday: boolean;
  isWeekend: boolean;
  loginCount: number;
  isFirstLogin: boolean;
}

interface LoginStorageData {
  count: number;
  lastLogin: string;
  firstLogin: string;
}

const DynamicGreeting: React.FC<DynamicGreetingProps> = ({ userName, className = '', style = {}, sx = {} }) => {
  const [greeting, setGreeting] = useState<string>('');

  useEffect(() => {
    const greetingData = getGreetingData();
    const message = generateGreeting(greetingData, userName);
    setGreeting(message);
  }, [userName]);

  // Function to get time-related data
  const getTimeData = () => {
    // Get current time in IST (Indian Standard Time - UTC+5:30)
    const istTime = dayjs().tz(IST_TIMEZONE);
    const hour = istTime.hour();
    const day = istTime.day(); // 0 = Sunday, 1 = Monday, etc.
    const dayOfWeek = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'][day];

    // Determine time of day based on IST
    let timeOfDay: 'morning' | 'afternoon' | 'evening' | 'night';
    if (hour >= TIME_OF_DAY.MORNING_START && hour < TIME_OF_DAY.AFTERNOON_START) {
      timeOfDay = 'morning';
    } else if (hour >= TIME_OF_DAY.AFTERNOON_START && hour < TIME_OF_DAY.EVENING_START) {
      timeOfDay = 'afternoon';
    } else if (hour >= TIME_OF_DAY.EVENING_START && hour < TIME_OF_DAY.NIGHT_START) {
      timeOfDay = 'evening';
    } else {
      timeOfDay = 'night';
    }

    return {
      istTime,
      hour,
      day,
      dayOfWeek,
      timeOfDay,
      today: istTime.format('YYYY-MM-DD'),
    };
  };

  // Function to handle localStorage interactions for login tracking
  const manageLoginTracking = (istTime: dayjs.Dayjs, today: string) => {
    const storageKey = `${LOGIN_STORAGE_KEY_PREFIX}${today}`;
    let loginData: string | null = null;
    let parsedLoginData: LoginStorageData | null = null;
    let loginCount = 1;

    // Get existing login data
    try {
      loginData = localStorage.getItem(storageKey);
    } catch (error) {
      console.error('Error getting login data from localStorage:', error);
    }

    // Parse and increment login count
    if (loginData) {
      try {
        parsedLoginData = JSON.parse(loginData);
        loginCount = (parsedLoginData?.count || 0) + 1;
      } catch (error) {
        console.error('Error parsing login data:', error);
        // Clean up corrupted data
        try {
          localStorage.removeItem(storageKey);
        } catch (removeError) {
          console.error('Error removing corrupted data:', removeError);
        }
      }
    }

    // Update login count and track login times
    try {
      localStorage.setItem(
        storageKey,
        JSON.stringify({
          count: loginCount,
          lastLogin: istTime.toISOString(),
          firstLogin: parsedLoginData?.firstLogin || istTime.toISOString(),
        })
      );
    } catch (error) {
      console.error('Error setting login data in localStorage:', error);
    }

    // Update global last seen
    try {
      localStorage.setItem(LAST_SEEN_STORAGE_KEY, istTime.toISOString());
    } catch (error) {
      console.error('Error setting last seen in localStorage:', error);
    }

    // Clean up old login data (keep only last 7 days)
    cleanupOldLoginData();

    return {
      loginCount,
      isFirstLogin: loginCount === 1,
    };
  };

  // Main function to orchestrate and return greeting data
  const getGreetingData = (): GreetingData => {
    const timeData = getTimeData();
    const loginData = manageLoginTracking(timeData.istTime, timeData.today);

    return {
      timeOfDay: timeData.timeOfDay,
      dayOfWeek: timeData.dayOfWeek,
      isMonday: timeData.day === 1,
      isTuesday: timeData.day === 2,
      isWednesday: timeData.day === 3,
      isThursday: timeData.day === 4,
      isFriday: timeData.day === 5,
      isWeekend: timeData.day === 0 || timeData.day === 6,
      loginCount: loginData.loginCount,
      isFirstLogin: loginData.isFirstLogin,
    };
  };

  const cleanupOldLoginData = () => {
    try {
      const keys = Object.keys(localStorage);
      const loginKeys = keys.filter((key) => key.startsWith(LOGIN_STORAGE_KEY_PREFIX));
      const istTime = dayjs().tz(IST_TIMEZONE);

      loginKeys.forEach((key) => {
        const dateStr = key.replace(LOGIN_STORAGE_KEY_PREFIX, ''); // This will be YYYY-MM-DD
        const loginDate = dayjs(dateStr); // This parses as local midnight
        const daysDiff = istTime.diff(loginDate, 'day');

        if (daysDiff > LOGIN_DATA_RETENTION_DAYS) {
          localStorage.removeItem(key);
        }
      });
    } catch (error) {
      console.error('Error cleaning up login data:', error);
    }
  };

  const getRandomGreeting = (greetings: string[]): string => {
    return greetings[Math.floor(Math.random() * greetings.length)];
  };

  const generateGreeting = (data: GreetingData, name: string): string => {
    const { timeOfDay, dayOfWeek, isMonday, isTuesday, isWednesday, isThursday, isFriday, isWeekend, loginCount, isFirstLogin } = data;
    const firstName = name?.split(' ')[0] || 'there';

    // 1. FIRST LOGIN - ONLY DAILY TIME-OF-DAY GREETINGS
    if (isFirstLogin) {
      if (timeOfDay === 'morning') {
        const greetings = [
          `Good morning, ${firstName}! Ready to tackle the day? ☀️`,
          `Rise and shine, ${firstName}! Let's make today great! 🌅`,
          `Morning, ${firstName}! What shall we accomplish today? ✨`,
        ];
        return getRandomGreeting(greetings);
      } else if (timeOfDay === 'afternoon') {
        const greetings = [
          `Good afternoon, ${firstName}! How can I help you today? 🌤️`,
          `Afternoon, ${firstName}! Ready to tackle your tasks? 💼`,
          `Hey ${firstName}! Perfect time to get things done! ⚡`,
        ];
        return getRandomGreeting(greetings);
      } else if (timeOfDay === 'evening') {
        const greetings = [
          `Good evening, ${firstName}! Working late or just getting started? 🌆`,
          `Evening, ${firstName}! Time to wrap things up or power through? 🌇`,
          `Hey ${firstName}! Evening hustle time! 🌃`,
        ];
        return getRandomGreeting(greetings);
      }
      // If first login but night time, fall through to default
      return `Welcome back, ${firstName}! How can I assist you today? 👋`;
    }

    // 2-4. MULTIPLE LOGINS - Collect applicable greeting categories
    const applicableGreetings: string[][] = [];

    // 2. MULTIPLE VISIT MESSAGES (More than 3 logins)
    if (loginCount > FREQUENT_LOGIN_THRESHOLD) {
      applicableGreetings.push([
        `You're really active today, ${firstName}!<br/>Love the energy! 🔥`,
        `Back again, ${firstName}?<br/>You're unstoppable! 💪`,
        `Multiple visits today!<br/>${firstName}, you're crushing it! 🚀`,
        `${firstName}, you're on a roll today!<br/>Keep it up! ⚡`,
        `Back for more, ${firstName}?<br/>What can I help with? 💼`,
        `Another visit, ${firstName}!<br/>Ready to continue? ⚡`,
        `You're making the most of today, ${firstName}!<br/>Impressive focus 💪`,
      ]);
    }

    // 3. WEEKDAY MESSAGES (Monday-Friday)
    if (!isWeekend) {
      if (isMonday) {
        applicableGreetings.push([
          `Monday energy, ${firstName}!<br/>Let's make this week amazing! 💪`,
          `New week, new opportunities,<br/>${firstName}! Let's go! 🌟`,
          `Monday reset, ${firstName}!<br/>Time to jump into a new week! 🔄`,
        ]);
      } else if (isTuesday) {
        applicableGreetings.push([
          `Tuesday momentum, ${firstName}!<br/>Keep it going! 💼`,
          `Great to see you this Tuesday, ${firstName}! 🚀`,
          `Tuesday focus, ${firstName}!<br/>Building on yesterday's progress 📈`,
        ]);
      } else if (isWednesday) {
        applicableGreetings.push([
          `Hump day, ${firstName}!<br/>You're halfway there! 🏔️`,
          `Wednesday vibes, ${firstName}!<br/>Let's keep the momentum! ⚡`,
          `Midweek check-in, ${firstName}!<br/>How's it going? 💪`,
        ]);
      } else if (isThursday) {
        applicableGreetings.push([
          `Thursday energy, ${firstName}!<br/>Weekend is coming! 🌟`,
          `Great Thursday, ${firstName}!<br/>One more day to go! 🚀`,
          `Thursday drive, ${firstName}!<br/>Almost at the finish line 🏁`,
        ]);
      } else if (isFriday) {
        applicableGreetings.push([
          `TGIF, ${firstName}!<br/>Weekend is almost here! 🏁`,
          `Friday vibes, ${firstName}!<br/>You've got this! ✨`,
          `Friday finish, ${firstName}!<br/>Let's end the week strong! 🎉`,
        ]);
      }
    }

    // 4. WEEKEND MESSAGES (Saturday-Sunday)
    if (isWeekend) {
      applicableGreetings.push([
        `Weekend mode, ${firstName}! Taking care of business on ${dayOfWeek}! 👏`,
        `${dayOfWeek} dedication, ${firstName}! That's impressive! 🌟`,
        `Working on ${dayOfWeek}, ${firstName}? You're amazing! 💫`,
        `${dayOfWeek} hustle, ${firstName}! Respect the commitment! 🏆`,
      ]);
    }

    // If we have applicable greetings, randomly pick a category and then a message
    if (applicableGreetings.length > 0) {
      const selectedCategory = applicableGreetings[Math.floor(Math.random() * applicableGreetings.length)];
      return getRandomGreeting(selectedCategory);
    }

    // Default fallback
    return `Welcome back, ${firstName}! How can I assist you today? 👋`;
  };

  return (
    <Typography
      sx={{
        fontSize: 'var(--ds-text-display)',
        color: 'var(--ds-brand-600)',
        fontWeight: 'var(--ds-font-weight-medium)',
        '@media (max-width: 1300px)': {
          fontSize: 'var(--ds-text-heading)',
        },
        ...sx,
      }}
      className={className}
      style={style}
    >
      {greeting.split('<br/>').map((line, index, arr) => (
        <React.Fragment key={index}>
          {line}
          {index < arr.length - 1 && <br />}
        </React.Fragment>
      ))}
    </Typography>
  );
};

export default DynamicGreeting;
