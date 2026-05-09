import gzip
import logging
import textwrap
from copy import deepcopy
from datetime import datetime
from typing import Any, Callable, Dict, List, Optional, Sequence

from datetime import timezone

from pydantic import BaseModel, ConfigDict

try:
    from tabulate import tabulate
except ImportError:

    def tabulate(*args, **kwargs):
        raise ImportError("Please install tabulate to use the TableBlock")


from notifications_server.message_templates.base import BaseBlock

BLOCK_SIZE_LIMIT = 2997
PRINTED_TABLE_MAX_WIDTH = 70
DEFAULT_TIMEZONE = timezone.utc


class RendererType:
    DATETIME = "DATETIME"


def render_value(renderer: RendererType, value):
    if renderer == RendererType.DATETIME:
        date_value = datetime.fromtimestamp(value / 1000.0)
        return date_value.astimezone(DEFAULT_TIMEZONE).strftime("%b %d, %Y, %I:%M:%S %p")
    raise ValueError(f"Unsupported renderer type {renderer}")


class MarkdownBlock(BaseBlock):
    """
    A Block of `Markdown <https://en.wikipedia.org/wiki/Markdown>`__
    """

    text: str

    def __init__(self, text: str, dedent: bool = False):
        """
        :param text: one or more paragraphs of Markdown markup
        :param dedent: if True, remove common indentation so that you can use multi-line docstrings.
        """
        if dedent:
            if text[0] == "\n":
                text = text[1:]
            text = textwrap.dedent(text)

        if len(text) >= BLOCK_SIZE_LIMIT:
            text = text[:BLOCK_SIZE_LIMIT] + "..."
        super().__init__(text=text)


class ContextBlock(BaseBlock):
    text: str


class DividerBlock(BaseBlock):
    """
    A visual separator between other blocks
    """

    pass


class ActionElement(BaseModel):
    element: BaseBlock


class ActionListBlock(BaseBlock):
    elements: List[ActionElement] = []


class FileBlock(BaseBlock):
    """
    A file of any type. Used for images, log files, binary files, and more.
    """

    filename: str
    contents: bytes

    def __init__(
        self,
        filename: str,
        contents: bytes,
        **kwargs,
    ):
        """
        :param filename: the file's name
        :param contents: the file's contents
        """
        super().__init__(
            filename=filename,
            contents=contents,
            **kwargs,
        )

    def is_text_file(self):
        return self.filename.endswith((".txt", ".log"))

    def zip(self):
        try:
            self.contents = gzip.compress(self.contents)
            self.filename = self.filename + ".gz"
        except Exception as exc:
            logging.error(f"Unexpected error occurred while zipping file {self.filename}")
            logging.exception(exc)

    def truncate_content(self, max_file_size_bytes: int) -> bytes:
        """
        Truncates the log file by removing lines from the beginning until its size is within the given limit.
        """
        # we don't want to truncate other files like images
        if not self.is_text_file():
            return self.contents

        decoded_content = self.contents.decode("utf-8")
        content_length = len(decoded_content)

        if content_length <= max_file_size_bytes:
            return self.contents

        lines = decoded_content.splitlines()
        byte_length_newline = len("\n".encode("utf-8"))

        truncated_lines: List[str] = []
        for idx, line in enumerate(lines):
            line_content_length = len(line) + byte_length_newline

            content_length -= line_content_length
            if content_length <= max_file_size_bytes:
                truncated_lines = lines[idx + 1 :]
                break

        return "\n".join(truncated_lines).encode("utf-8")


class HeaderBlock(BaseBlock):
    """
    Text formatted as a header
    """

    text: str

    def __init__(self, text: str):
        """
        :param text: the header
        """
        super().__init__(text=text)


class ListBlock(BaseBlock):
    """
    A list of items, nicely formatted
    """

    items: List[str]

    def __init__(self, items: List[str]):
        """
        :param items: a list of strings
        """
        super().__init__(items=items)

    def to_markdown(self) -> MarkdownBlock:
        mrkdwn = [f" {item}" for item in self.items]
        return MarkdownBlock("\n".join(mrkdwn))


class JsonBlock(BaseBlock):
    """
    Json data
    """

    json_str: str

    def __init__(self, json_str: str):
        """
        :param json_str: json as a string
        """
        super().__init__(json_str=json_str)


class TableBlock(BaseBlock):
    """
    Table display of a list of lists.

    Note: Wider tables appears as a file attachment on Slack, because they aren't rendered properly inline

    :var column_width: Hint to sink for the portion of size each column should use. Not supported by all sinks.
        example: [1, 1, 1, 2] use twice the size for last column.
    """

    rows: List[List]
    headers: Sequence[str] = ()
    column_renderers: Dict = {}
    table_name: str = ""
    column_width: Optional[List[int]] = None

    def __init__(
        self,
        rows: List[List],
        headers: Sequence[str] = (),
        column_renderers: Dict = {},
        table_name: str = "",
        column_width: List[int] = None,
        **kwargs,
    ):
        """
        :param rows: a list of rows. each row is a list of columns
        :param headers: names of each column
        """
        super().__init__(
            rows=rows,
            headers=headers,
            column_renderers=column_renderers,
            table_name=table_name,
            column_width=column_width,
            **kwargs,
        )

    @classmethod
    def __calc_max_width(cls, headers, rendered_rows, table_max_width: int) -> List[int]:
        # We need to make sure the total table width, doesn't exceed the max width,
        # otherwise, the table is printed corrupted
        columns_max_widths = [len(header) for header in headers]
        for row in rendered_rows:
            for idx, val in enumerate(row):
                columns_max_widths[idx] = max(len(str(val)), columns_max_widths[idx])

        if sum(columns_max_widths) > table_max_width:  # We want to limit the widest column
            largest_width = max(columns_max_widths)
            widest_column_idx = columns_max_widths.index(largest_width)
            diff = sum(columns_max_widths) - table_max_width
            columns_max_widths[widest_column_idx] = largest_width - diff
            if columns_max_widths[widest_column_idx] < 0:  # in case the diff is bigger than the largest column
                # just divide equally
                columns_max_widths = [
                    int(table_max_width / len(columns_max_widths)) for _ in range(0, len(columns_max_widths))
                ]

        return columns_max_widths

    @classmethod
    def __trim_rows(cls, contents: str, max_chars: int):
        # We need to make sure that the total character count doesn't exceed max_chars,
        # but if we cut off a row in the middle then it messes up the whole table.
        # So instead remove entire rows at a time
        if len(contents) <= max_chars:
            return contents

        truncator = "\n..."
        max_chars -= len(truncator)

        lines = contents.splitlines()
        length_so_far = 0
        lines_to_include = 0
        for line in lines:
            new_length = length_so_far + len("\n") + len(line)
            if new_length > max_chars:
                break
            else:
                length_so_far = new_length
                lines_to_include += 1

        return "\n".join(lines[:lines_to_include]) + truncator

    @classmethod
    def __to_strings_rows(cls, rows):
        # This is just to assert all row column values are strings. Tabulate might fail on other types
        return [[str(column_value) for column_value in row] for row in rows]

    def to_markdown(self, max_chars=None, add_table_header: bool = True) -> MarkdownBlock:
        table_header = f"{self.table_name}\n" if self.table_name else ""
        table_header = "" if not add_table_header else table_header
        prefix = f"{table_header}```\n"
        suffix = "\n```"
        table_contents = self.to_table_string()
        if max_chars is not None:
            max_chars = max_chars - len(prefix) - len(suffix)
            table_contents = self.__trim_rows(table_contents, max_chars)

        return MarkdownBlock(f"{prefix}{table_contents}{suffix}")

    def to_table_string(self, table_max_width: int = PRINTED_TABLE_MAX_WIDTH) -> str:
        rendered_rows = self.__to_strings_rows(self.render_rows())
        col_max_width = self.__calc_max_width(self.headers, rendered_rows, table_max_width)
        return tabulate(
            rendered_rows,
            headers=self.headers,
            tablefmt="presto",
            maxcolwidths=col_max_width,
        )

    def render_rows(self) -> List[List]:
        if self.column_renderers is None:
            return self.rows
        new_rows = deepcopy(self.rows)
        for column_name, renderer_type in self.column_renderers.items():
            column_idx = self.headers.index(column_name)
            for row in new_rows:
                row[column_idx] = render_value(renderer_type, row[column_idx])
        return new_rows


class CallbackChoice(BaseModel):
    action: Callable
    action_params: Optional[BaseModel] = None

    model_config = ConfigDict(arbitrary_types_allowed=True)


class CallbackBlock(BaseBlock):
    """
    A set of buttons that allows callbacks from the sink - for example, a button in Slack that will trigger
    another action when clicked
    """

    choices: Dict[str, CallbackChoice]

    def __init__(self, choices: Dict[str, CallbackChoice]):
        """
        :param choices: a dict mapping between each the text on each button to the action it triggers
        """
        super().__init__(choices=choices)


class LinkProp(BaseModel):
    text: str
    url: str


class LinksBlock(BaseBlock):
    """
    A set of links
    """

    links: List[LinkProp] = []
