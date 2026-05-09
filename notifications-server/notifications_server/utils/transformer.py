import base64
import io
import json
import logging
import os
import re
import tempfile
import urllib.parse
import gzip
from typing import List, Optional, Dict, Any

import markdown2

from notifications_server.message_templates.blocks import (
    BaseBlock,
    DividerBlock,
    FileBlock,
    HeaderBlock,
    JsonBlock,
    ListBlock,
    MarkdownBlock,
    TableBlock,
    CallbackBlock,
    LinksBlock,
    CallbackChoice,
    LinkProp,
    ActionListBlock,
    ContextBlock,
)
from notifications_server.utils.callbacks import ExternalActionRequestBuilder

ACTION_TRIGGER_PLAYBOOK = "trigger_playbook"
ACTION_LINK = "link"
SlackBlock = Dict[str, Any]
MAX_BLOCK_CHARS = 3000
SLACK_SIGNIN_SECRET = os.environ.get("SLACK_SIGNING_SECRET", "signing_secret")

try:
    from tabulate import tabulate
except ImportError:

    def tabulate(*args, **kwargs):
        raise ImportError("Please install tabulate to use the TableBlock")


LOG = logging.getLogger(__name__)


class Transformer:
    def __init__(self, slack_app, teams_app, session, token=None, engine=None):
        super().__init__(engine)
        self.slack_app = slack_app
        self.teams_app = teams_app
        self._session = session
        self._engine = engine
        self.token = token

    @staticmethod
    def apply_length_limit(msg: str, max_length: int, truncator: Optional[str] = None) -> str:
        """
        Method that crops the string if it is bigger than max_length provided.
        Args:
            msg: The string that needs to be truncated.
            max_length: Max length of the string allowed
            truncator: truncator string that will be appended, if max length is exceeded.

        Examples:

            >>> print(Transformer.apply_length_limit('1234567890', 9))
            123456...

            >>> print(Transformer.apply_length_limit('1234567890', 9, "."))
            12345678.

        Returns:
            Croped string with truncator appended at the end if length is exceeded.
            The original string otherwise

        """
        if len(msg) <= max_length:
            return msg
        truncator = truncator or "..."
        return msg[: max_length - len(truncator)] + truncator

    @staticmethod
    def _format_json_value(value: Any, show_preview: bool = True, depth: int = 0) -> str:
        if isinstance(value, dict):
            return Transformer._format_dict_value(value, show_preview, depth)
        elif isinstance(value, list):
            return Transformer._format_list_value(value, show_preview, depth)
        elif isinstance(value, str):
            return Transformer._format_string_value(value)
        else:
            return json.dumps(value)

    @staticmethod
    def _format_dict_value(value: dict, show_preview: bool, depth: int) -> str:
        if not show_preview or len(value) == 0:
            return f"dict[{len(value)} items]"
        if depth >= 2:
            return f"dict[{len(value)} items]"

        first_key = next(iter(value))
        first_val = Transformer._format_nested_value(value[first_key])
        remaining = len(value) - 1
        suffix = f", ... {remaining} more" if remaining > 0 else ""
        return f"{{{first_key}: {first_val}{suffix}}}"

    @staticmethod
    def _format_list_value(value: list, show_preview: bool, depth: int) -> str:
        if not show_preview or len(value) == 0:
            return f"list[{len(value)} items]"

        first_item = value[0]
        if isinstance(first_item, dict) and depth < 2:
            return Transformer._format_object_array(value)
        elif isinstance(first_item, str):
            return Transformer._format_string_array(value)
        else:
            return Transformer._format_generic_array(value)

    @staticmethod
    def _format_nested_value(value: Any) -> str:
        if isinstance(value, str) and len(value) > 30:
            return f'"{value[:30]}..."'
        elif isinstance(value, (dict, list)):
            return f"{type(value).__name__}[{len(value)}]"
        else:
            return json.dumps(value)

    @staticmethod
    def _format_object_array(value: list) -> str:
        first_item = value[0]
        keys = list(first_item.keys())[:3]
        key_preview = ", ".join(f'"{k}"' for k in keys)
        remaining_keys = len(first_item) - len(keys)
        key_suffix = f", ... {remaining_keys} more" if remaining_keys > 0 else ""
        item_repr = f"{{{key_preview}{key_suffix}}}"
        remaining_items = len(value) - 1
        suffix = f", ... {remaining_items} more" if remaining_items > 0 else ""
        return f"[{item_repr}{suffix}]"

    @staticmethod
    def _format_string_array(value: list) -> str:
        first_item = value[0]
        if len(first_item) > 30:
            first_item = f'"{first_item[:30]}..."'
        else:
            first_item = json.dumps(first_item)
        remaining = len(value) - 1
        suffix = f", ... {remaining} more" if remaining > 0 else ""
        return f"[{first_item}{suffix}]"

    @staticmethod
    def _format_generic_array(value: list) -> str:
        first_repr = json.dumps(value[0])
        remaining = len(value) - 1
        suffix = f", ... {remaining} more" if remaining > 0 else ""
        return f"[{first_repr}{suffix}]"

    @staticmethod
    def _format_string_value(value: str) -> str:
        if len(value) > 100:
            return f'"{value[:100]}..."'
        return json.dumps(value)

    @staticmethod
    def _truncate_dict(items: List, max_length: int) -> str:
        """Truncate a dictionary for display."""
        if not items:
            return "{}"

        result_parts = ["{"]
        current_length = 1
        max_items = 10  # Increased from 5 to 10

        for i, (key, value) in enumerate(items[:max_items]):
            if i > 0:
                result_parts.append(", ")
                current_length += 2

            value_str = Transformer._format_json_value(value)
            pair = f'"{key}": {value_str}'

            if current_length + len(pair) > max_length - 50:
                result_parts.append(f"... {len(items) - i} more fields")
                break

            result_parts.append(pair)
            current_length += len(pair)
        else:
            if len(items) > max_items:
                result_parts.append(f"... {len(items) - max_items} more fields")

        result_parts.append("}")
        return "".join(result_parts)

    @staticmethod
    def _truncate_list(data: List, max_length: int) -> str:
        """Truncate a list for display."""
        if not data:
            return "[]"

        result_parts = ["["]
        current_length = 1
        max_items = 5  # Increased from 3 to 5

        for i, item in enumerate(data[:max_items]):
            if i > 0:
                result_parts.append(", ")
                current_length += 2

            item_str = Transformer._format_json_value(item)

            if current_length + len(item_str) > max_length - 50:
                result_parts.append(f"... {len(data) - i} more items")
                break

            result_parts.append(item_str)
            current_length += len(item_str)
        else:
            if len(data) > max_items:
                result_parts.append(f"... {len(data) - max_items} more items")

        result_parts.append("]")
        return "".join(result_parts)

    @staticmethod
    def smart_truncate_json(data: Any, max_length: int = 500) -> str:
        """
        Intelligently truncate JSON data for Slack messages.

        Args:
            data: JSON data (dict, list, or string)
            max_length: Maximum character length for the output

        Returns:
            Formatted, truncated string representation
        """
        try:
            # If it's already a string, try to parse it
            if isinstance(data, str):
                try:
                    data = json.loads(data)
                except ValueError:
                    return Transformer.apply_length_limit(data, max_length)

            # Handle different data types
            if isinstance(data, dict):
                return Transformer._truncate_dict(list(data.items()), max_length)
            elif isinstance(data, list):
                return Transformer._truncate_list(data, max_length)
            else:
                # Handle primitive types
                result = json.dumps(data)
                return Transformer.apply_length_limit(result, max_length)

        except Exception as e:
            LOG.error(f"Error in smart_truncate_json: {e}")
            # Fallback to string representation
            return Transformer.apply_length_limit(str(data), max_length)

    @staticmethod
    def get_markdown_links(markdown_data: str) -> List[str]:
        regex = "<[^>]*\\|[^>]*>"
        matches = re.findall(regex, markdown_data)
        links = []
        if matches:
            links = [match for match in matches if len(match) > 1]  # filter out illegal matches
        return links

    @staticmethod
    def to_github_markdown(markdown_data: str, add_angular_brackets: bool = True) -> str:
        """Transform all occurrences of slack markdown, <URL|LINK TEXT>, to github markdown [LINK TEXT](URL)."""
        # some markdown parsers doesn't support angular brackets on links
        OPENING_ANGULAR = "<" if add_angular_brackets else ""
        CLOSING_ANGULAR = ">" if add_angular_brackets else ""
        matches = Transformer.get_markdown_links(markdown_data)
        for match in matches:
            # take only the data between the first '<' and last '>'
            splits = match[1:-1].split("|")
            if len(splits) == 2:  # don't replace unexpected strings
                parsed_url = urllib.parse.urlparse(splits[0])
                parsed_url = parsed_url._replace(path=urllib.parse.quote_plus(parsed_url.path, safe="/"))
                replacement = f"[{splits[1]}]({OPENING_ANGULAR}{parsed_url.geturl()}{CLOSING_ANGULAR})"
                markdown_data = markdown_data.replace(match, replacement)
        return re.sub(r"\*([^\*]*)\*", r"**\1**", markdown_data)

    @staticmethod
    def to_slack_markdown_link(github_markdown: str) -> str:
        """
        Transform GitHub markdown [LINK TEXT](URL) to Slack markdown <URL|LINK TEXT>.
        """
        # Replace GitHub-style links with Slack-style links
        github_link_pattern = r"\[([^\]]+)\]\(([^)]+)\)"
        slack_markdown = re.sub(github_link_pattern, r"<\2|\1>", github_markdown)

        # Replace GitHub-style bold with Slack-style bold
        slack_markdown = re.sub(r"\*\*([^\*]+)\*\*", r"*\1*", slack_markdown)

        return slack_markdown

    @staticmethod
    def slack_markdown_to_generic_markdown(slack_text: str) -> str:
        """
        Transform Slack markdown to generic markdown format suitable for LLM processing.

        Conversions:
        - <URL|LINK TEXT> -> [LINK TEXT](URL)
        - *bold* -> **bold**
        - _italic_ -> *italic*
        - ~strikethrough~ -> ~~strikethrough~~
        - `code` -> `code` (unchanged)
        - ```code block``` -> ```code block``` (unchanged)
        - <@USER_ID> -> @user (user mentions - should already be replaced by caller)
        - <!channel>, <!here>, <!everyone> -> @channel, @here, @everyone
        - Blockquotes (lines starting with >) preserved
        - Lists preserved

        Args:
            slack_text: Text in Slack markdown format

        Returns:
            Text in generic markdown format
        """
        if not slack_text:
            return slack_text

        result = slack_text

        # Convert Slack links <URL|TEXT> to markdown [TEXT](URL)
        # This handles both bare URLs and URLs with display text
        link_pattern = r"<(https?://[^|>]+)\|([^>]+)>"
        result = re.sub(link_pattern, r"[\2](\1)", result)

        # Convert bare URLs in angle brackets <URL> to markdown [URL](URL)
        bare_url_pattern = r"<(https?://[^>]+)>"
        result = re.sub(bare_url_pattern, r"[\1](\1)", result)

        # Convert special mentions
        result = result.replace("<!channel>", "@channel")
        result = result.replace("<!here>", "@here")
        result = result.replace("<!everyone>", "@everyone")

        # Convert Slack bold (*text*) to markdown bold (**text**)
        # Need to be careful not to convert italic or list markers
        # Match *text* but not when preceded/followed by another *
        result = re.sub(r"(?<!\*)\*(?!\*)([^\*\n]+?)(?<!\*)\*(?!\*)", r"**\1**", result)

        # Convert Slack italic (_text_) to markdown italic (*text*)
        result = re.sub(r"_([^_\n]+?)_", r"*\1*", result)

        # Slack strikethrough (~text~) to markdown (~~text~~)
        result = re.sub(r"~([^~\n]+?)~", r"~~\1~~", result)

        # Code blocks and inline code are already compatible, no changes needed

        return result

    @staticmethod
    def markdown_to_slack_markdown(markdown_text: str) -> str:
        lines = markdown_text.strip().split("\n")
        slack_lines = []
        for line in lines:
            # Convert headers
            if line.startswith("###"):
                slack_lines.append(f"*{line[4:].strip()}*")
            elif line.startswith("##"):
                slack_lines.append(f"*{line[3:].strip()}*")
            elif line.startswith("#"):
                slack_lines.append(f"*{line[2:].strip()}*")
            # Convert bold
            elif "**" in line:
                # Handles bold
                line = re.sub(r"\*\*(.*?)\*\*", r"*\1*", line)
                slack_lines.append(line)
            # Convert unordered list
            elif line.strip().startswith("* "):
                slack_lines.append(f"• {line.strip()[2:]}")
            # Convert tables
            elif line.strip().startswith("|") and "---" not in line:
                cols = [col.strip() for col in line.strip("|").split("|")]
                slack_lines.append(" | ".join(cols))
            elif "---" in line:
                continue
            else:
                slack_lines.append(line.strip())

        return "\n".join(slack_lines)

    @classmethod
    def __markdown_to_html(cls, mrkdwn_text: str) -> str:
        # replace links: from <http://url|name> to <a href="url">name</a>
        mrkdwn_links = re.findall(r"<[^\\|]*\|[^\>]*>", mrkdwn_text)
        for link in mrkdwn_links:
            link_content = link[1:-1]
            link_parts = link_content.split("|")
            mrkdwn_text = mrkdwn_text.replace(link, f'<a href="{link_parts[0]}">{link_parts[1]}</a>')

        # replace slack markdown bold: from *bold text* to <b>bold text<b>  (markdown2 converts this to italic)
        mrkdwn_text = re.sub(r"\*([^\*]*)\*", r"<b>\1</b>", mrkdwn_text)

        # Note - markdown2 should be used after slack links already converted, otherwise it's getting corrupted!
        # Convert other markdown content
        return markdown2.markdown(mrkdwn_text)

    @classmethod
    def to_html(cls, blocks: List[BaseBlock]) -> str:
        lines = []
        for block in blocks:
            if isinstance(block, MarkdownBlock):
                if not block.text:
                    continue
                lines.append(f"{cls.__markdown_to_html(block.text)}")
            elif isinstance(block, DividerBlock):
                lines.append("-------------------")
            elif isinstance(block, JsonBlock):
                lines.append(block.json_str)
            elif isinstance(block, HeaderBlock):
                lines.append(f"<strong>{block.text}</strong>")
            elif isinstance(block, ListBlock):
                lines.extend(cls.__markdown_to_html(block.to_markdown().text))
            elif isinstance(block, TableBlock):
                if block.table_name:
                    lines.append(cls.__markdown_to_html(block.table_name))
                lines.append(tabulate(block.render_rows(), headers=block.headers, tablefmt="html").replace("\n", ""))
        return "\n".join(lines)

    @classmethod
    def to_standard_markdown(cls, blocks: List[BaseBlock]) -> str:
        lines = []
        for block in blocks:
            if isinstance(block, MarkdownBlock):
                if not block.text:
                    continue
                lines.append(f"{cls.to_github_markdown(block.text, False)}")
            elif isinstance(block, DividerBlock):
                lines.append("-------------------")
            elif isinstance(block, JsonBlock):
                lines.append(block.json_str)
            elif isinstance(block, HeaderBlock):
                lines.append(f"**{block.text}**")
            elif isinstance(block, ListBlock):
                lines.extend(cls.to_github_markdown(block.to_markdown().text, False))
            elif isinstance(block, TableBlock):
                if block.table_name:
                    lines.append(cls.to_github_markdown(block.table_name, False))
                rendered_rows = block.render_rows()
                lines.append(tabulate(rendered_rows, headers=block.headers, tablefmt="presto"))
        return "\n".join(lines)

    @staticmethod
    def tableblock_to_fileblocks(blocks: List[BaseBlock], column_limit: int) -> List[FileBlock]:
        file_blocks: List[FileBlock] = []
        for table_block in [b for b in blocks if isinstance(b, TableBlock)]:
            if len(table_block.headers) >= column_limit:
                table_name = table_block.table_name if table_block.table_name else "data"
                table_content = table_block.to_table_string(table_max_width=250)  # bigger max width for file
                file_blocks.append(FileBlock(f"{table_name}.txt", bytes(table_content, "utf-8")))
                blocks.remove(table_block)

        return file_blocks

    @staticmethod
    def extract_text_from_base64_gz(encoded_data):
        try:
            encoded_data = encoded_data[2:-1]
            decoded_data = base64.b64decode(encoded_data)
            return gzip.decompress(decoded_data)
        except Exception as e:
            print(f"Error extracting text: {e}")
            return None

    @staticmethod
    def extract_text_from_base64(encoded_data):
        try:
            encoded_data = encoded_data[2:-1]
            decoded_data = base64.b64decode(encoded_data)
            return decoded_data
        except Exception as e:
            print(f"Error extracting text: {e}")
            return None

    @staticmethod
    def json_to_slack_blocks(data, type_):
        blocks = []
        if type_ == "markdown":
            blocks.append(MarkdownBlock(text=data))
        elif type_ == "table":
            headers = data["headers"]
            rows = data["rows"]
            column_renderers = data.get("column_renderers", None)
            table_block = TableBlock(
                headers=headers,
                rows=rows,
                column_renderers=column_renderers,
            )
            blocks.append(table_block)
        # Removed automatic divider to prevent multiple horizontal lines in evidences
        return blocks

    @staticmethod
    def __to_slack_links(links: List[LinkProp]) -> List[SlackBlock]:
        if len(links) == 0:
            return []

        buttons = []
        for i, link in enumerate(links):
            buttons.append(Transformer.__to_link_button(i, link))

        return [{"type": "actions", "elements": buttons}]

    @staticmethod
    def __to_link_button(i, link):
        return {
            "type": "button",
            "text": {
                "type": "plain_text",
                "text": link.text,
            },
            "action_id": f"{ACTION_LINK}_{i}",
            "url": link.url,
        }

    @staticmethod
    def __get_action_block_for_choices(choices: Dict[str, CallbackChoice] = None):
        if choices is None:
            return []

        buttons = []
        for i, (text, callback_choice) in enumerate(choices.items()):
            buttons.append(Transformer.__to_callback_button(callback_choice, i, text))

        return [{"type": "actions", "elements": buttons}]

    @staticmethod
    def __to_callback_button(callback_choice, i, text):
        return {
            "type": "button",
            "text": {
                "type": "plain_text",
                "text": text,
            },
            "style": "primary",
            "action_id": f"{ACTION_TRIGGER_PLAYBOOK}_{text}",
            "value": ExternalActionRequestBuilder.create_for_func(
                callback_choice,
                text,
                SLACK_SIGNIN_SECRET,
            ).model_dump_json(),
        }

    @staticmethod
    def __to_slack_action_list(block: ActionListBlock):
        if block is None:
            return []

        buttons = []
        for action in block.elements:
            if isinstance(action.element, CallbackBlock):
                for i, (text, callback_choice) in enumerate(action.element.choices.items()):
                    buttons.append(Transformer.__to_callback_button(callback_choice, i, text))
            if isinstance(action.element, LinksBlock):
                for i, link in enumerate(action.element.links):
                    buttons.append(Transformer.__to_link_button(i, link))

        return [{"type": "actions", "elements": buttons}]

    @staticmethod
    def __to_slack_markdown(block: MarkdownBlock) -> List[SlackBlock]:
        if not block.text:
            return []

        return [
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": Transformer.apply_length_limit(block.text, MAX_BLOCK_CHARS),
                },
            }
        ]

    @staticmethod
    def __to_slack_context(block: ContextBlock) -> List[SlackBlock]:
        if not block.text:
            return []

        return [
            {
                "type": "context",
                "elements": [
                    {
                        "type": "mrkdwn",
                        "text": Transformer.apply_length_limit(block.text, MAX_BLOCK_CHARS),
                    }
                ],
            }
        ]

    @staticmethod
    def __to_slack_header(block):
        return [
            {
                "type": "header",
                "text": {
                    "type": "plain_text",
                    "text": Transformer.apply_length_limit(block.text, 150),
                    "emoji": True,
                },
            }
        ]

    @staticmethod
    def __to_slack_table(block: TableBlock):
        if len(block.headers) == 2:
            table_rows: List[str] = []
            for row in block.rows:
                if "-------" in str(row[1]):  # special care for table sub-header
                    subheader: str = row[0]
                    table_rows.append(f"--- {subheader.capitalize()} ---")
                    continue

                header = f"*{row[0]}*"
                value_str = str(row[1])
                if len(value_str) <= 200:
                    value = f"`{value_str}`"
                else:
                    value = f"```{value_str[:200]}```"
                table_rows.append(f"• {header} {value}")

            table_str = "\n".join(table_rows)
            table_str = f"{block.table_name} \n{table_str}"
            return Transformer.__to_slack_markdown(MarkdownBlock(table_str))

        return Transformer.__to_slack_markdown(block.to_markdown())

    @staticmethod
    def _upload_file_to_slack(slack_app, installation, block: FileBlock, max_file_size_kb: int) -> str:
        """Upload a file to slack and return a link to it."""
        truncated_content = block.truncate_content(max_file_size_bytes=max_file_size_kb * 1000)

        with tempfile.NamedTemporaryFile() as f:
            f.write(truncated_content)
            f.flush()
            result = slack_app.client.file_upload(
                token=installation.token, title=block.filename, fname=f.name, filename=block.filename
            )
            return result["file"]["permalink"]

    @staticmethod
    def prepare_slack_text(slack_app, installation, message: str, max_file_size_kb: int, files: List[FileBlock] = []):
        if files:
            # it's a little annoying but it seems like files need to be referenced in `title` and not just `blocks`
            # in order to be actually shared. well, I'm actually not sure about that, but when I tried adding the files
            # to a separate block and not including them in `title` or the first block then the link was present but
            # the file wasn't actually shared and the link was broken
            uploaded_files = []
            for file_block in files:
                # slack throws an error if you write empty files, so skip it
                if len(file_block.contents) == 0:
                    continue
                permalink = Transformer._upload_file_to_slack(
                    slack_app, installation, file_block, max_file_size_kb=max_file_size_kb
                )
                uploaded_files.append(f"* <{permalink} | {file_block.filename}>")

            file_references = "\n".join(uploaded_files)
            message = f"{message}\n{file_references}"

        if len(message) == 0:
            return "empty-message"  # blank messages aren't allowed

        return Transformer.apply_length_limit(message, MAX_BLOCK_CHARS)

    @staticmethod
    def to_slack(block: BaseBlock) -> List[SlackBlock]:
        if isinstance(block, MarkdownBlock):
            return Transformer.__to_slack_markdown(block)
        if isinstance(block, ContextBlock):
            return Transformer.__to_slack_context(block)
        elif isinstance(block, DividerBlock):
            return [{"type": "divider"}]
        elif isinstance(block, FileBlock):
            raise AssertionError("to_slack() should never be called on a FileBlock")
        elif isinstance(block, HeaderBlock):
            return Transformer.__to_slack_header(block)
        elif isinstance(block, TableBlock):
            return Transformer.__to_slack_table(block)
        elif isinstance(block, ListBlock):
            return Transformer.__to_slack_markdown(block.to_markdown())
        elif isinstance(block, LinksBlock):
            return Transformer.__to_slack_links(block.links)
        elif isinstance(block, CallbackBlock):
            return Transformer.__get_action_block_for_choices(block.choices)
        elif isinstance(block, ActionListBlock):
            return Transformer.__to_slack_action_list(block)
        else:
            LOG.debug(f"cannot convert block of type {type(block)} to slack format block: {block}")
            return []  # no reason to crash the entire report

    @staticmethod
    def json_to_teams_table(input_data):
        # Extract relevant data from the input
        rows = input_data["rows"]
        headers = input_data["headers"]

        # Create the Adaptive Card table structure
        adaptive_card_table = {
            "type": "Table",
            "gridStyle": "emphasis",
            "firstRowAsHeaders": True,
            "columns": [{"width": 1} for _ in headers],
            "rows": [
                {
                    "type": "TableRow",
                    "cells": [
                        {
                            "type": "TableCell",
                            "items": [{"type": "TextBlock", "text": header, "wrap": True, "weight": "Bolder"}],
                        }
                        for header in headers
                    ],
                },
                *[
                    {
                        "type": "TableRow",
                        "cells": [
                            {
                                "type": "TableCell",
                                "items": [
                                    {
                                        "type": "TextBlock",
                                        "text": str(row_data).replace('"', "*"),
                                        "wrap": True,
                                    }
                                ],
                            }
                            for row_data in row
                        ],
                    }
                    for row in rows
                ],
            ],
        }

        return adaptive_card_table

    @staticmethod
    def json_to_markdown_table(data):
        headers = data["headers"]
        rows = data["rows"][:7]  # limit to 10 rows

        if len(headers) == 2:
            table_rows: List[str] = []
            for row in rows:
                if "-------" in str(row[1]):  # special care for table sub-header
                    subheader: str = row[0]
                    table_rows.append(f"--- {subheader.capitalize()} ---")
                    continue

                if len(str(row[1])) > 200:
                    table_rows.append(f"● {row[0]} ```{row[1]}```")
                else:
                    table_rows.append(f"● {row[0]} `{row[1]}`")

            table_str = "\n".join(table_rows)
            return table_str
        else:
            # Create header row
            header_row = " | ".join(headers) + "\n"
            separator_row = "|".join(["---" for _ in range(len(headers))]) + "\n"

            # Create rows
            body_rows = ""
            for row in rows:
                body_row = "|".join([str(cell) for cell in row]) + "\n"
                body_rows += body_row

            # Combine all parts to form the table
            markdown_table = (
                "\n\n" + data["table_name"] + "\n\n" + "```" + header_row + separator_row + body_rows + "```"
            )

            return markdown_table


def add_text_info_evidence(data):
    if not isinstance(data, str):
        data = str(data)
    return Transformer.apply_length_limit(data, 1000)
